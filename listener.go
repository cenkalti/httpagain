package httpagain

import (
	"errors"
	"net"
	"sync"
)

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
