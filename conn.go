package httpagain

import (
	"net"
	"time"
)

// timeoutConn wraps a net.Conn, and sets a deadline for every read and write operation.
type timeoutConn struct {
	net.Conn
	readTimeout  time.Duration
	writeTimeout time.Duration
}

func (c *timeoutConn) Read(b []byte) (int, error) {
	if c.readTimeout > 0 {
		err := c.Conn.SetReadDeadline(time.Now().Add(c.readTimeout))
		if err != nil {
			return 0, err
		}
	}
	return c.Conn.Read(b)
}

func (c *timeoutConn) Write(b []byte) (int, error) {
	if c.writeTimeout > 0 {
		err := c.Conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
		if err != nil {
			return 0, err
		}
	}
	return c.Conn.Write(b)
}
