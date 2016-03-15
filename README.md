# httpagain

httpagain is for building HTTP servers that restarts gracefully.
This is possible thanks to https://github.com/rcrowley/goagain.


Send SIGUSR2 to a process and it will restart without downtime.
httpagain uses double-fork strategy as default to keep same PID after restart.
This plays nicely with process managers such as upstart, supervisord, etc.


Send SIGTERM for graceful shutdown.



## Usage

Demo HTTP Server with graceful termination and restart:
https://github.com/cenkalti/httpagain/blob/master/httpagaindemo/demo.go

1. Install the demo application

        go get github.com/cenkalti/httpagain/httpagaindemo

1. Start it in the first terminal

        httpagaindemo

   This will output something like:

        pid:42633 01:06:15.240136 httpagain.go:56: listening on [::]:8080

1. In a second terminal start a slow HTTP request

        curl 'http://localhost:8080/sleep?duration=20s'

1. In a third terminal trigger a graceful server restart (using the pid from your output):

        kill -USR2 42633

    Now, the process has forked to wait for current requests to finish.
    By this time new forked process can still accept new requests.

1. Trigger another shorter request that finishes before the earlier request:

        curl 'http://localhost:8080/sleep?duration=0s'

    This will output something like:

        request slept for 1s from pid 42874.

    Note that this second quick request is served by the new process
    (as indicated by the PID) while the slow first request will be served by
    the first server.

1. After first request is finished run the second quick requst again to see that it will be served by the same old PID:

        curl 'http://localhost:8080/sleep?duration=0s'

    This will output something like:

        request slept for 1s from pid 42633.
