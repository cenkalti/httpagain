// Package httpagain is for building HTTP servers that restarts gracefully.
// This is possible thanks to github.com/rcrowley/goagain package.
// Send SIGUSR2 to a process and it will restart without downtime.
// httpagain uses double-fork strategy as default to keep same PID after restart.
// This plays nicely with process managers such as upstart, supervisord, etc.
// Send SIGTERM for graceful shutdown.
package httpagain

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/rcrowley/goagain"
)

// GracePeriod is the duration to wait for active requests and running goroutines
// to finish before restarting/shutting down the server. Set 0 to disable.
var GracePeriod = 30 * time.Second

// TCPReadTimeout for read operations on connections. Set 0 to disable.
var TCPReadTimeout = 30 * time.Second

// TCPWriteTimeout for write operations on connections. Set 0 to disable.
var TCPWriteTimeout = 30 * time.Second

const breakAcceptInterval = 100 * time.Millisecond

var goroutineWG sync.WaitGroup

func init() {
	// Use double-fork strategy from goagain package.
	// After restart pid does not changes.
	// This plays nicely with process managers such as upstart, supervisord, etc.
	goagain.Strategy = goagain.Double

	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
	log.SetPrefix(fmt.Sprintf("pid:%d ", syscall.Getpid()))
}

// Begin must be called before spawning new goroutine from request handlers.
func Begin() { goroutineWG.Add(1) }

// End must be called at the end of goroutines spawned from request handlers.
// It is recommended to call End() at the beginning of a goroutine with a defer statement.
func End() { goroutineWG.Done() }

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
	stopAccept := make(chan struct{})
	var acceptWG, requestWG sync.WaitGroup
	acceptWG.Add(1)
	l, err := goagain.Listener()
	if err != nil {
		l, err = net.Listen("tcp", addr)
		if err != nil {
			log.Fatalln(err)
		}

		log.Println("listening on", l.Addr())
		go acceptLoop(l, stopAccept, srv, &acceptWG, &requestWG)
	} else {
		log.Println("resuming listening on", l.Addr())
		go acceptLoop(l, stopAccept, srv, &acceptWG, &requestWG)

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

	// Signal the goroutine to stop accepting connections and wait for acceptLoop() to finish.
	// This does not take more than breakAcceptInterval.
	close(stopAccept)
	acceptWG.Wait()

	// Wait for active requests and goroutines to finish.
	done := make(chan struct{})
	go func() {
		requestWG.Wait()
		goroutineWG.Wait()
		close(done)
	}()

	var graceDone <-chan time.Time
	if GracePeriod > 0 {
		graceDone = time.After(GracePeriod)
	}

	select {
	case <-graceDone:
		log.Println("some requests/goroutines did not finish in allowed period, they will be killed")
	case <-done:
		// Requests/goroutines are finished in allowed time.
	}

	// If we received SIGUSR2, re-exec the parent process.
	if goagain.SIGUSR2 == sig {
		if err := goagain.Exec(l); err != nil {
			log.Fatalln(err)
		}
	}
}

func acceptLoop(l net.Listener, stopAccept chan struct{}, srv *http.Server, acceptWG, requestWG *sync.WaitGroup) {
	// Wrap original handler so it decrements the wait group counter after handling the request.
	srvCopy := *srv
	srv = &srvCopy
	srv.Handler = wrapHandler(srv.Handler, requestWG)

	defer acceptWG.Done()
	for {

		// Break out of the accept loop on the next iteration after the
		// process was signaled and our channel was closed.
		select {
		case <-stopAccept:
			return
		default:
		}

		// Set a deadline so Accept doesn't block forever, which gives
		// us an opportunity to stop gracefully.
		err := l.(*net.TCPListener).SetDeadline(time.Now().Add(breakAcceptInterval))
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
		requestWG.Add(1)
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

func wrapHandler(h http.Handler, wg *sync.WaitGroup) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer wg.Done()
		h.ServeHTTP(w, r)
	})
}
