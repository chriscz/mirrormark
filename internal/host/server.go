package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/ws"
)

// helloWait is how long a connection may take from the upgrade to a right
// proof. A program that connects and says nothing is dropped after it,
// rather than holding a connection open for ever. A variable so that tests
// can shorten it.
var helloWait = 10 * time.Second

// Listen listens on 127.0.0.1 on the first of ports it can bind.
func Listen(ports []int) (net.Listener, error) {
	var errs []error
	for _, port := range ports {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err == nil {
			return ln, nil
		}
		errs = append(errs, err)
	}
	return nil, fmt.Errorf("cannot listen on 127.0.0.1 on any of the ports %v: %w", ports, errors.Join(errs...))
}

// Serve accepts extension connections on ln until ctx ends and runs a host
// for each. Hosts share nothing in memory: the global lock keeps their
// rounds apart, as it kept separate processes apart, since flock locks
// belong to the open file and not to the process.
func Serve(ctx context.Context, ln net.Listener, o Options) error {
	if o.Ping == 0 {
		o.Ping = 20 * time.Second
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return err
	}
	var conns atomic.Int64
	// hosts counts the running handlers. mu and closing keep a handler that
	// starts while Serve shuts down from adding to hosts during Wait.
	var hosts sync.WaitGroup
	var mu sync.Mutex
	closing := false
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Counted from the start, so Serve waits for a host that is
			// still writing its database file when ctx ends.
			mu.Lock()
			if closing {
				mu.Unlock()
				http.Error(w, "shutting down", http.StatusServiceUnavailable)
				return
			}
			hosts.Add(1)
			mu.Unlock()
			defer hosts.Done()
			log := o.Log.With("conn", conns.Add(1))
			origin, ok := admit(r, ln.Addr().String(), o.Paths)
			if !ok {
				log.Warn("refused a connection", "host", r.Host, "origin", r.Header.Get("Origin"))
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			sock, err := ws.Upgrade(w, r, maxIn)
			if err != nil {
				log.Warn("bad handshake", "err", err)
				return
			}
			// Until it proves itself, a connection has helloWait to do so.
			sock.SetReadDeadline(time.Now().Add(helloWait))
			c := newWSConn(sock, o.Ping)
			ho := o
			ho.Log, ho.Origin, ho.Port = log, origin, port
			err = Run(ctx, c, ho)
			c.close()
			if errors.Is(err, os.ErrDeadlineExceeded) {
				log.Warn("dropped a connection that did not prove itself in time", "wait", helloWait)
				return
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("host stopped", "err", err)
				return
			}
			log.Info("disconnected")
		}),
	}
	go func() {
		<-ctx.Done()
		srv.Close() // stops accepting; hijacked connections end with their hosts
	}()
	err = srv.Serve(ln)
	mu.Lock()
	closing = true
	mu.Unlock()
	hosts.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// admit checks a handshake before the upgrade and returns its Origin. The
// Host must be the address the server listens on, which a page that
// rebinds its own name to 127.0.0.1 cannot send, and the Origin must be a
// database's, which no web page has. Any program can send any Origin;
// the challenge stops those.
func admit(r *http.Request, addr string, p db.Paths) (string, bool) {
	origin := r.Header.Get("Origin")
	if r.Host != addr || origin == "" {
		return "", false
	}
	// A database that cannot be read cannot be matched; the rounds that
	// read it warn about it.
	dbs, _ := db.LoadAll(p)
	for _, d := range dbs {
		if d.Profile.Origin == origin {
			return origin, true
		}
	}
	return "", false
}

// wsConn is a Conn over one WebSocket. It sends a ping message every
// interval: Chromium keeps an extension's service worker running while
// messages arrive, and the extension answers each ping with an extension
// API call, which keeps a Gecko event page running too.
type wsConn struct {
	sock   *ws.Conn
	stop   chan struct{}
	once   sync.Once
	closed atomic.Bool // close has been called
}

func newWSConn(sock *ws.Conn, ping time.Duration) *wsConn {
	c := &wsConn{sock: sock, stop: make(chan struct{})}
	go func() {
		t := time.NewTicker(ping)
		defer t.Stop()
		for {
			select {
			case <-c.stop:
				return
			case <-t.C:
				if c.Write(Msg{V: Version, Type: "ping"}) != nil {
					return
				}
			}
		}
	}()
	return c
}

func (c *wsConn) Read() (Msg, error) {
	b, err := c.sock.Read()
	if err != nil {
		// A Read that our own close cut short is the end of the
		// connection, not a failure.
		if c.closed.Load() && errors.Is(err, net.ErrClosed) {
			return Msg{}, io.EOF
		}
		return Msg{}, err
	}
	return decode(b)
}

func (c *wsConn) Write(m Msg) error {
	b, err := encode(m)
	if err != nil {
		return err
	}
	return c.sock.Write(b)
}

// proven ends the hello wait: the host calls it once the extension has
// proven which database it is.
func (c *wsConn) proven() {
	c.sock.SetReadDeadline(time.Time{}) // fails only once closed, when Read fails anyway
}

func (c *wsConn) close() {
	c.once.Do(func() {
		c.closed.Store(true)
		close(c.stop)
		c.sock.Close(1000, "")
	})
}
