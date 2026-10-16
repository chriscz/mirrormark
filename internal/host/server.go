package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
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

// helloMax is the largest message a connection may send before its
// proof: a hello or a proof is a few hundred bytes, so a program that has
// proven nothing cannot make the server buffer more.
const helloMax = 4 << 10

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
// for each. Hosts share nothing in memory but which config.yaml was last
// logged as loaded: the global lock keeps their rounds apart, as it kept
// separate processes apart, since flock locks belong to the open file and
// not to the process. If ln fails, Serve stops its hosts and returns the
// error.
func Serve(ctx context.Context, ln net.Listener, o Options) error {
	if o.Ping == 0 {
		o.Ping = 20 * time.Second
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	o.config = &configLog{}
	sv := &server{ctx: ctx, o: o, addr: ln.Addr().String(), port: port}
	sv.refused.msg, sv.unproven.msg = "refused a connection", "dropped a connection that did not prove itself"
	// Only the handshake goes through HTTP, so its headers are small, and a
	// keep-alive connection that only got refused is closed once idle.
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, IdleTimeout: helloWait, MaxHeaderBytes: 8 << 10, Handler: sv}
	go func() {
		<-ctx.Done()
		srv.Close() // stops accepting; hijacked connections end with their hosts
	}()
	err = srv.Serve(ln)
	cancel() // a failed listener stops the hosts too
	sv.hosts.closeAndWait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// server answers each handshake on Serve's listener.
type server struct {
	ctx      context.Context // ends every host
	o        Options
	addr     string // the address Serve listens on, which the Host must name
	port     string
	conns    atomic.Int64 // numbers the connections in the log
	refused  refusals     // handshakes admit turned away
	unproven refusals     // admitted connections that never proved themselves
	hosts    hostGroup
}

func (sv *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Counted from the start, so Serve waits for a host that is still
	// writing its database file when ctx ends.
	if !sv.hosts.enter() {
		http.Error(w, "shutting down", http.StatusServiceUnavailable)
		return
	}
	defer sv.hosts.leave()
	log := sv.o.Log.With("conn", sv.conns.Add(1))
	origin, ok := admit(r, sv.addr, sv.o.Paths)
	if !ok {
		sv.refused.log(r.Context(), log, slog.String("host", r.Host), slog.String("origin", r.Header.Get("Origin")))
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	sock, err := ws.Upgrade(w, r, helloMax)
	if err != nil {
		log.Debug("bad handshake", "err", err)
		sv.unproven.log(r.Context(), log, slog.String("why", "bad handshake"))
		return
	}
	// Until it proves itself, a connection has helloWait to do so.
	sock.SetReadDeadline(time.Now().Add(helloWait))
	c := newWSConn(sock, sv.o.Ping)
	ho := sv.o
	ho.Log, ho.Origin, ho.Port = log, origin, sv.port
	err = Run(sv.ctx, c, ho)
	c.close()
	if !c.ok.Load() {
		// Not known to be mirrormark: logged as briefly as a refusal,
		// with a fixed reason, never in the peer's words.
		var st stopped
		why := "closed"
		switch {
		case errors.As(err, &st):
			why = st.code
		case errors.Is(err, os.ErrDeadlineExceeded):
			why = "no proof within " + helloWait.String()
		case errors.Is(err, context.Canceled):
			return // the server is stopping
		case err != nil:
			log.Debug("unproven connection failed", "err", err)
			why = "read failed"
		}
		sv.unproven.log(r.Context(), log, slog.String("why", why))
		return
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Warn("host stopped", "err", err)
		return
	}
	log.Info("disconnected")
}

// hostGroup counts the handlers that are running. Once closed it lets no
// more in, so its Wait never races a handler that starts during shutdown.
type hostGroup struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// enter counts a handler in, or reports false once the group is closed.
func (g *hostGroup) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	return true
}

func (g *hostGroup) leave() { g.wg.Done() }

// closeAndWait lets no more handlers in and waits for those running.
func (g *hostGroup) closeAndWait() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.wg.Wait()
}

// admit checks a handshake before the upgrade and returns its Origin. The
// Host must be the address the server listens on, which a page that
// rebinds its own name to 127.0.0.1 cannot send, and the Origin must be a
// database's, which no web page has. Any program can send any Origin;
// the challenge stops those.
func admit(r *http.Request, addr string, p db.Paths) (string, bool) {
	origin := r.Header.Get("Origin")
	if r.Host != addr || !strings.HasPrefix(origin, "chrome-extension://") && !strings.HasPrefix(origin, "moz-extension://") {
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

// refusals logs connections turned away: each at Debug, and at Info at
// most once a minute with how many there were since the last Info line. A
// program that keeps knocking costs a line a minute, and the first refusal
// still shows.
type refusals struct {
	mu   sync.Mutex
	n    int
	last time.Time
	msg  string // the line's message: what was done with the connection
}

func (f *refusals) log(ctx context.Context, log *slog.Logger, attrs ...slog.Attr) {
	log.LogAttrs(ctx, slog.LevelDebug, f.msg, attrs...)
	f.mu.Lock()
	f.n++
	if !f.last.IsZero() && time.Since(f.last) < time.Minute {
		f.mu.Unlock()
		return
	}
	n := f.n
	f.n, f.last = 0, time.Now()
	f.mu.Unlock()
	log.LogAttrs(ctx, slog.LevelInfo, f.msg, append(attrs, slog.Int("count", n))...)
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
	ok     atomic.Bool // the peer has proven itself
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

// proven ends the hello wait and lifts the hello's size limit: the host
// calls it once the extension has proven which database it is.
// The host calls it between Reads: see Run.
func (c *wsConn) proven() {
	c.ok.Store(true)
	c.sock.SetReadDeadline(time.Time{}) // fails only once closed, when Read fails anyway
	c.sock.SetReadLimit(maxIn)
}

func (c *wsConn) close() {
	c.once.Do(func() {
		c.closed.Store(true)
		close(c.stop)
		c.sock.Close(1000, "")
	})
}
