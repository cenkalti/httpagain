package main

import (
	"fmt"
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
	time.Sleep(duration)
	fmt.Fprintf(
		w,
		"request slept for %s from pid %d.\n",
		duration,
		os.Getpid(),
	)
}
