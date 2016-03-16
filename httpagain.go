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

var (
	// RequestGracePeriod is the duration to wait for active requests
	// to finish before restarting/shutting down the server. Set 0 to wait indefinitely.
	RequestGracePeriod = 30 * time.Second

	// GoroutineGracePeriod is the duration to wait for running goroutines (tracked with Begin() and End() calls)
	// to finish before restarting/shutting down the server. Set 0 to wait indefinitely.
	GoroutineGracePeriod = 30 * time.Second

	// TCPReadTimeout for read operations on connections. Set 0 to disable.
	TCPReadTimeout = 30 * time.Second

	// TCPWriteTimeout for write operations on connections. Set 0 to disable.
	TCPWriteTimeout = 30 * time.Second

	// Shutdown channel will be closed when a signal is received.
	Shutdown = make(chan struct{})
)

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

	var acceptWG, requestWG sync.WaitGroup

	// Wrap original request handler to track active requests.
	var srvCopy = *srv
	srv = &srvCopy
	srv.Handler = wrapHandler(srv.Handler, &requestWG)

	// Inherit a net.Listener from our parent process or listen anew.
	acceptWG.Add(1)
	l, err := goagain.Listener()
	if err != nil {
		l, err = net.Listen("tcp", addr)
		if err != nil {
			log.Fatalln(err)
		}

		log.Println("listening on", l.Addr())
		go acceptLoop(l, srv, &acceptWG, &requestWG)
	} else {
		log.Println("resuming listening on", l.Addr())
		go acceptLoop(l, srv, &acceptWG, &requestWG)

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
	close(Shutdown)

	var allDoneWG sync.WaitGroup
	allDoneWG.Add(3)
	go timeoutWaitGroup(&allDoneWG, &acceptWG, 0, "")
	go timeoutWaitGroup(&allDoneWG, &requestWG, RequestGracePeriod, "some requests did not finish in allowed period, they will be killed")
	go timeoutWaitGroup(&allDoneWG, &goroutineWG, GoroutineGracePeriod, "some goroutines did not finish in allowed period, they will be killed")
	allDoneWG.Wait()

	// If we received SIGUSR2, re-exec the parent process.
	if goagain.SIGUSR2 == sig {
		if err := goagain.Exec(l); err != nil {
			log.Fatalln(err)
		}
	}
}

func timeoutWaitGroup(allDoneWG, wg *sync.WaitGroup, timeout time.Duration, timeoutMsg string) {
	doneWG := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneWG)
	}()
	var timeoutChan <-chan time.Time
	if timeout > 0 {
		timeoutChan = time.After(timeout)
	}
	select {
	case <-doneWG:
	case <-timeoutChan:
		log.Println(timeoutMsg)
	}
	allDoneWG.Done()
}

func acceptLoop(l net.Listener, srv *http.Server, acceptWG, requestWG *sync.WaitGroup) {
	defer acceptWG.Done()
	for {

		// Break out of the accept loop on the next iteration after the
		// process was signaled and our channel was closed.
		select {
		case <-Shutdown:
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
