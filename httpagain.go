// Package httpagain is for building HTTP servers that restarts gracefully.
// This is possible thanks to github.com/rcrowley/goagain package.
// Send SIGUSR2 to a process and it will restart without downtime.
// httpagain uses double-fork strategy as default to keep same PID after restart.
// This plays nicely with process managers such as upstart, supervisord, etc.
package httpagain

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/rcrowley/goagain"
)

func init() {
	// Use double-fork strategy from goagain package.
	// After restart pid does not changes.
	// This plays nicely with process managers such as upstart, supervisord, etc.
	goagain.Strategy = goagain.Double

	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
	log.SetPrefix(fmt.Sprintf("pid:%d ", syscall.Getpid()))
}

// ListenAndServe is similar to http.ListenAndServe.
// It listens on the TCP network address addr then calls srv.Serve to handle requests on incoming connections.
// If addr is blank, ":http" is used.
// If srv is blank, a server with handler http.DefaultServeMux is used.
// ListenAndServe exits fatally if there is an error.
func ListenAndServe(addr string, srv *http.Server) {
	// Set default values.
	if addr == "" {
		addr = ":http"
	}
	if srv == nil {
		srv = &http.Server{Addr: addr, Handler: http.DefaultServeMux}
	}

	// Inherit a net.Listener from our parent process or listen anew.
	ch := make(chan struct{})
	wg := &sync.WaitGroup{}
	wg.Add(1)
	l, err := goagain.Listener()
	if err != nil {
		// Listen on a TCP or a UNIX domain socket (TCP here).
		l, err = net.Listen("tcp", addr)
		if err != nil {
			log.Fatalln(err)
		}
		log.Println("listening on", l.Addr())

		// Accept connections in a new goroutine.
		go serve(l, ch, srv, wg)
	} else {
		// Resume listening and accepting connections in a new goroutine.
		log.Println("resuming listening on", l.Addr())
		go serve(l, ch, srv, wg)

		// If this is the child, send the parent SIGUSR2.  If this is the
		// parent, send the child SIGQUIT.
		if err = goagain.Kill(); err != nil {
			log.Fatalln(err)
		}
	}

	// Block the main goroutine awaiting signals.
	sig, err := goagain.Wait(l)
	if err != nil {
		log.Fatalln(err)
	}

	// Do whatever's necessary to ensure a graceful exit like waiting for
	// goroutines to terminate or a channel to become closed.
	//
	// In this case, we'll close the channel to signal the goroutine to stop
	// accepting connections and wait for the goroutine to exit.
	close(ch)
	wg.Wait()

	// If we received SIGUSR2, re-exec the parent process.
	if goagain.SIGUSR2 == sig {
		if err := goagain.Exec(l); err != nil {
			log.Fatalln(err)
		}
	}
}

func serve(l net.Listener, ch chan struct{}, srv *http.Server, wg *sync.WaitGroup) {
	// Wrap original handler so it decrements the wait group counter after handling the request.
	srvCopy := *srv
	srv = &srvCopy
	srv.Handler = wrapHandler(srv.Handler, wg)

	defer wg.Done()
	for {

		// Break out of the accept loop on the next iteration after the
		// process was signaled and our channel was closed.
		select {
		case <-ch:
			return
		default:
		}

		// Set a deadline so Accept doesn't block forever, which gives
		// us an opportunity to stop gracefully.
		err := l.(*net.TCPListener).SetDeadline(time.Now().Add(100 * time.Millisecond))
		if err != nil {
			log.Fatalln(err)
		}

		c, err := l.Accept()
		if err != nil {
			if goagain.IsErrClosing(err) {
				return
			}
			if err.(*net.OpError).Timeout() {
				continue
			}
			log.Fatalln(err)
		}

		// Server will spawn a goroutine for connection and will return with errSingleListen.
		wg.Add(1)
		sl := &singleListener{l: l, conn: c}
		err = srv.Serve(sl)
		if err == errSingleListen {
			continue
		}
		if err != nil {
			log.Fatalln(err)
		}
	}
}

// errSingleListen is returned on second call to Accept().
var errSingleListen = errors.New("errSingleListen")

// singleListener is a net.Listener that returns a single connection.
type singleListener struct {
	l    net.Listener
	conn net.Conn
	once sync.Once
}

func (s *singleListener) Accept() (net.Conn, error) {
	var c net.Conn
	s.once.Do(func() {
		c = s.conn
	})
	if c != nil {
		return c, nil
	}
	return nil, errSingleListen
}

func (s *singleListener) Close() (err error) {
	s.once.Do(func() {
		err = s.conn.Close()
	})
	return
}

func (s *singleListener) Addr() net.Addr {
	return s.l.Addr()
}

func wrapHandler(h http.Handler, wg *sync.WaitGroup) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer wg.Done()
		h.ServeHTTP(w, r)
	})
}
