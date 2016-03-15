package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/cenkalti/httpagain"
)

func main() {
	http.HandleFunc("/sleep", handleSleep)
	httpagain.ListenAndServe(":8080", nil)
}

func handleSleep(w http.ResponseWriter, r *http.Request) {
	duration, err := time.ParseDuration(r.FormValue("duration"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	if r.FormValue("go") == "" {
		// Request will block for duration.
		sleepAndLog(w, duration)
	} else {
		// Request is returned immediately but gorotine will sleep for duration.
		httpagain.Begin()
		go sleepAndLogGraceful(duration)
	}
}

func sleepAndLog(w http.ResponseWriter, duration time.Duration) {
	pid := os.Getpid()
	fmt.Fprintf(w, "pid: %d sleeping for %s...\n", pid, duration)
	w.(http.Flusher).Flush()
	time.Sleep(duration)
	fmt.Fprintf(w, "pid: %d slept for %s\n", pid, duration)
}

func sleepAndLogGraceful(duration time.Duration) {
	defer httpagain.End()

	pid := os.Getpid()
	log.Printf("pid: %d sleeping for %s...\n", pid, duration)
	time.Sleep(duration)
	log.Printf("pid: %d slept for %s\n", pid, duration)
}
