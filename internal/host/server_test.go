package host_test

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/fakebrowser"
	"github.com/chriscz/mirrormark/internal/host"
	"github.com/chriscz/mirrormark/internal/ws"
)

// bound writes a database file for id with org and token.
func bound(t *testing.T, p db.Paths, id, org, token string) {
	d := &db.DB{Format: 1, Profile: db.Profile{ID: id, Label: "chrome/" + id, Browser: "chrome", Origin: org, Token: token}}
	if err := db.SaveDB(p.ProfileFile(id), d); err != nil {
		t.Fatal(err)
	}
}

// serve runs Serve on a free port until the test ends and returns its
// address.
func serve(t *testing.T, p db.Paths, ping time.Duration) string {
	return serveLog(t, p, ping, io.Discard)
}

// serveLog is serve with the server's log, at debug level, going to w.
func serveLog(t *testing.T, p db.Paths, ping time.Duration, w io.Writer) string {
	ln, err := host.Listen([]int{0})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- host.Serve(ctx, ln, host.Options{Paths: p, Log: slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})),
			Timeout: 300 * time.Millisecond, Poll: 20 * time.Millisecond, Ping: ping})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return ln.Addr().String()
}

func dialAs(t *testing.T, addr, hostHeader, org string) (*ws.Conn, error) {
	h := http.Header{"Origin": {org}}
	if hostHeader != "" {
		h.Set("Host", hostHeader)
	}
	c, err := ws.Dial(addr, h, 1<<20)
	if err == nil {
		t.Cleanup(func() { c.Close(1000, "") })
	}
	return c, err
}

func send(t *testing.T, c *ws.Conn, m host.Msg) {
	m.V = host.Version
	b, _ := json.Marshal(m)
	if err := c.Write(b); err != nil {
		t.Fatal(err)
	}
}

// The tokens install would write: 64 lowercase hex digits.
var (
	tok1 = strings.Repeat("1a", 32)
	tok2 = strings.Repeat("2b", 32)
	tok3 = strings.Repeat("3c", 32)
)

// nonceE is the extension's nonce in these tests.
var nonceE = strings.Repeat("5a", 32)

// mac is the proof, written out again here so that a change to either
// side's input fails a test.
func mac(token, msg string) string {
	h := hmac.New(sha256.New, []byte(token))
	h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil))
}

// challenged sends a hello for id and checks that the server proved it
// holds token for this port and nonce. It returns the server's nonce.
func challenged(t *testing.T, c *ws.Conn, port, id, token string) string {
	t.Helper()
	send(t, c, host.Msg{Type: "hello", DB: id, Nonce: nonceE})
	m := next(t, c)
	if m.Type != "challenge" || len(m.Nonce) != 64 || m.Proof != mac(token, "server:"+port+":"+nonceE) {
		t.Fatalf("%s: got %+v, want a challenge with the server's proof", id, m)
	}
	return m.Nonce
}

// next returns the next message other than a ping. A server that sends
// nothing within 5 s fails the test rather than hanging it.
func next(t *testing.T, c *ws.Conn) host.Msg {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	for {
		b, err := c.Read()
		if err != nil {
			t.Fatal(err)
		}
		var m host.Msg
		json.Unmarshal(b, &m)
		if m.Type != "ping" {
			return m
		}
	}
}

func TestServeRefusesBeforeTheUpgrade(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	for _, c := range []struct{ name, host, origin string }{
		{"another host name", "localhost:" + port, testOrigin},
		{"a web page", "", "http://127.0.0.1:8000"},
		{"no origin", "", ""},
	} {
		if _, err := dialAs(t, addr, c.host, c.origin); err == nil || !strings.Contains(err.Error(), "403") {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// Without a binding for this origin the server has nothing to prove, so it
// answers with an error and no challenge.
func TestServeChallengesOnlyForABinding(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	bound(t, p, "untokened", testOrigin, "")
	bound(t, p, "other", "chrome-extension://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", tok3)
	addr := serve(t, p, time.Hour)
	for _, c := range []struct{ id, nonce, code string }{
		{"missing", nonceE, "db-missing"},
		{"untokened", nonceE, "bad-token"},
		{"other", nonceE, "bad-token"},
		{"a", "short", "bad-token"},
	} {
		conn, err := dialAs(t, addr, "", testOrigin)
		if err != nil {
			t.Fatal(err)
		}
		send(t, conn, host.Msg{Type: "hello", DB: c.id, Nonce: c.nonce})
		if m := next(t, conn); m.Type != "error" || m.Code != c.code {
			t.Errorf("%s: got %+v, want %s", c.id, m, c.code)
		}
	}
}

// A proof with no challenge waiting is ignored: it cannot stop the host,
// and the hello after it still gets a challenge.
func TestServeIgnoresAProofBeforeTheHello(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	c, err := dialAs(t, addr, "", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	send(t, c, host.Msg{Type: "proof", Proof: strings.Repeat("0", 64)})
	challenged(t, c, port, "a", tok1)
}

func TestServeRefusesAWrongProof(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	bound(t, p, "b", testOrigin, tok2)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	refused := func(name string, proof func(nonceS string) string) {
		t.Helper()
		c, err := dialAs(t, addr, "", testOrigin)
		if err != nil {
			t.Fatal(err)
		}
		nonceS := challenged(t, c, port, "a", tok1)
		send(t, c, host.Msg{Type: "proof", Proof: proof(nonceS)})
		if m := next(t, c); m.Type != "error" || m.Code != "bad-token" {
			t.Errorf("%s: got %+v", name, m)
		}
	}
	since := time.Now()
	refused("no proof", func(string) string { return "" })
	refused("another database's token", func(n string) string { return mac(tok2, "extension:"+n) })
	refused("the server's label", func(n string) string { return mac(tok1, "server:"+port+":"+n) })
	if db.Connected(p, "a", since) {
		t.Fatal("a refused proof counted as reporting in")
	}

	// A proof that was right once is refused against a new nonce.
	c, _ := dialAs(t, addr, "", testOrigin)
	old := mac(tok1, "extension:"+challenged(t, c, port, "a", tok1))
	send(t, c, host.Msg{Type: "proof", Proof: old})
	send(t, c, host.Msg{Type: "tree", Nodes: fakebrowser.New().Tree()})
	if m := next(t, c); m.Type == "error" {
		t.Fatalf("the right proof: %+v", m)
	}
	c.Close(1000, "")
	refused("a replayed proof", func(string) string { return old })
}

func TestServeRunsAHostPerConnection(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	bound(t, p, "b", testOrigin, tok2)
	writeConfig(t, p, workUnion)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	tokens := map[string]string{"a": tok1, "b": tok2}
	since := time.Now()
	for _, id := range []string{"a", "b"} {
		c, err := dialAs(t, addr, "", testOrigin)
		if err != nil {
			t.Fatal(err)
		}
		nonceS := challenged(t, c, port, id, tokens[id])
		// A tree before the proof is ignored, as one before a hello is.
		send(t, c, host.Msg{Type: "tree", Nodes: fakebrowser.New().Tree()})
		if db.Connected(p, id, since) {
			t.Fatalf("%s reported in before its proof", id)
		}
		send(t, c, host.Msg{Type: "proof", Proof: mac(tokens[id], "extension:"+nonceS)})
		send(t, c, host.Msg{Type: "tree", Nodes: fakebrowser.New().Tree()})
		// The first round sends the member folder's creation.
		if m := next(t, c); m.Type != "apply" {
			t.Fatalf("%s: got %+v", id, m)
		}
		if !db.Connected(p, id, since) {
			t.Fatalf("%s did not report in", id)
		}
	}
	// A second connection for a database in use is a copied profile.
	c, _ := dialAs(t, addr, "", testOrigin)
	nonceS := challenged(t, c, port, "a", tok1)
	send(t, c, host.Msg{Type: "proof", Proof: mac(tok1, "extension:"+nonceS)})
	if m := next(t, c); m.Code != "db-locked" {
		t.Fatalf("second connection for a: %+v", m)
	}
}

func TestServePings(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	c, err := dialAs(t, serve(t, p, 20*time.Millisecond), "", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Read()
	if err != nil || string(b) != `{"v":`+strconv.Itoa(host.Version)+`,"type":"ping"}` {
		t.Fatalf("%s %v", b, err)
	}
}

// portOf is the port of a listener on 127.0.0.1.
func portOf(t *testing.T, ln net.Listener) int {
	_, port, err := net.SplitHostPort(ln.Addr().String())
	n, err2 := strconv.Atoi(port)
	if err != nil || err2 != nil {
		t.Fatal(ln.Addr(), err, err2)
	}
	return n
}

func TestListenTakesTheFirstFreePort(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	freePort := portOf(t, free)
	free.Close()
	ln, err := host.Listen([]int{portOf(t, taken), freePort})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if got := portOf(t, ln); got != freePort {
		t.Fatalf("listening on %d, want %d", got, freePort)
	}
	if _, err := host.Listen([]int{portOf(t, taken)}); err == nil {
		t.Fatal("listened on a taken port")
	}
}

// A connection that never proves itself is dropped once the hello wait
// ends, before it takes any database's lock.
func TestServeDropsASilentConnection(t *testing.T) {
	host.SetHelloWait(t, 100*time.Millisecond)
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	silent, err := dialAs(t, addr, "", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	// One that says hello but never answers the challenge is dropped too.
	halfway, err := dialAs(t, addr, "", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	challenged(t, halfway, port, "a", tok1)
	for name, c := range map[string]*ws.Conn{"silent": silent, "no proof": halfway} {
		closed := make(chan error, 1)
		go func() {
			_, err := c.Read()
			closed <- err
		}()
		select {
		case err := <-closed:
			if err != io.EOF {
				t.Errorf("%s: %v, want the server's close", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: still open after the hello wait", name)
		}
	}
	for _, f := range []string{p.ProfileLock("a"), p.GlobalLock()} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s: %v, want no file", f, err)
		}
	}
}

// The hello wait ends with the proof: a proven connection may stay quiet.
func TestServeKeepsAProvenQuietConnection(t *testing.T) {
	host.SetHelloWait(t, 100*time.Millisecond)
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	bound(t, p, "b", testOrigin, tok2)
	writeConfig(t, p, workUnion)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	c, err := dialAs(t, addr, "", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	nonceS := challenged(t, c, port, "a", tok1)
	send(t, c, host.Msg{Type: "proof", Proof: mac(tok1, "extension:"+nonceS)})
	time.Sleep(300 * time.Millisecond)
	send(t, c, host.Msg{Type: "tree", Nodes: fakebrowser.New().Tree()})
	if m := next(t, c); m.Type != "apply" {
		t.Fatalf("got %+v", m)
	}
}

// Before its proof a connection is nobody: what it sends, other than the
// hello and the proof, is ignored, and none of it reaches the log as a
// warning in the peer's words.
func TestServeIgnoresMessagesBeforeTheProof(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	bound(t, p, "b", testOrigin, tok2)
	writeConfig(t, p, workUnion)
	log := &logBuf{}
	addr := serveLog(t, p, time.Hour, log)
	_, port, _ := net.SplitHostPort(addr)
	c, err := dialAs(t, addr, "", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	send(t, c, host.Msg{Type: "error", Code: "x", Message: "words of an unproven peer"})
	send(t, c, host.Msg{Type: "applied", Batch: 7})
	nonceS := challenged(t, c, port, "a", tok1)
	send(t, c, host.Msg{Type: "error", Code: "x", Message: "words of an unproven peer"})
	send(t, c, host.Msg{Type: "changed"})
	send(t, c, host.Msg{Type: "proof", Proof: mac(tok1, "extension:"+nonceS)})
	send(t, c, host.Msg{Type: "tree", Nodes: fakebrowser.New().Tree()})
	if m := next(t, c); m.Type != "apply" {
		t.Fatalf("got %+v", m)
	}
	if strings.Contains(log.String(), "unproven peer") || strings.Contains(log.String(), "late applied") {
		t.Fatalf("the log took in what came before the proof:\n%s", log)
	}
}

// rawConn upgrades by hand, so a test can send frame headers that ws.Conn
// never would.
func rawConn(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	nc.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(nc, "GET / HTTP/1.1\r\nHost: "+addr+"\r\nOrigin: "+testOrigin+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(nc)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake: %v %v", resp, err)
	}
	return nc, br
}

// closeCode reads the server's frames up to its close and returns the
// close code.
func closeCode(t *testing.T, br *bufio.Reader) uint16 {
	t.Helper()
	for {
		var h [2]byte
		if _, err := io.ReadFull(br, h[:]); err != nil {
			t.Fatal(err)
		}
		p := make([]byte, h[1]&0x7f) // the server sends nothing long here
		if _, err := io.ReadFull(br, p); err != nil {
			t.Fatal(err)
		}
		if h[0]&0x0f == 0x8 && len(p) >= 2 {
			return binary.BigEndian.Uint16(p)
		}
	}
}

// Before its proof a connection may send only small messages: a header
// that claims 1 MB is refused at once, before any of it is read.
func TestServeRefusesALargeMessageBeforeTheProof(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	nc, br := rawConn(t, serve(t, p, time.Hour))
	nc.Write([]byte{0x81, 0xff, 0, 0, 0, 0, 0, 0x10, 0, 0, 1, 2, 3, 4})
	if code := closeCode(t, br); code != 1009 {
		t.Fatalf("close code %d, want 1009", code)
	}
	if _, err := os.Stat(p.ProfileLock("a")); !os.IsNotExist(err) {
		t.Fatalf("lock file: %v", err)
	}
}

// The extension sends its tree straight after its proof, without waiting
// for an answer; the tree may be far larger than a hello.
func TestServeTakesALargeTreeRightAfterTheProof(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	bound(t, p, "b", testOrigin, tok2)
	writeConfig(t, p, workUnion)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	b := fakebrowser.New()
	for i := range 500 {
		b.Add("1", "bookmark "+strconv.Itoa(i), "https://example.com/"+strconv.Itoa(i))
	}
	tree, _ := json.Marshal(host.Msg{V: host.Version, Type: "tree", Nodes: b.Tree()})
	if len(tree) < 16<<10 {
		t.Fatalf("the tree is only %d bytes", len(tree))
	}
	for range 20 {
		c, err := dialAs(t, addr, "", testOrigin)
		if err != nil {
			t.Fatal(err)
		}
		nonceS := challenged(t, c, port, "a", tok1)
		send(t, c, host.Msg{Type: "proof", Proof: mac(tok1, "extension:"+nonceS)})
		if err := c.Write(tree); err != nil {
			t.Fatal(err)
		}
		if m := next(t, c); m.Type != "apply" {
			t.Fatalf("got %+v", m)
		}
		c.Close(1000, "")
		eventually(t, "a's lock to be free", func() bool {
			unlock, err := db.TryLock(p.ProfileLock("a"))
			if err == nil {
				unlock()
			}
			return err == nil
		})
	}
}

// Only an extension's origin can be a database's: a web page's is refused
// even if a profile block was edited to name it, and without reading any
// database.
func TestServeAdmitsOnlyExtensionOrigins(t *testing.T) {
	p := paths(t)
	bound(t, p, "web", "http://127.0.0.1:8000", tok1)
	addr := serve(t, p, time.Hour)
	if _, err := dialAs(t, addr, "", "http://127.0.0.1:8000"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("a web origin: %v", err)
	}
}

// Refusals are cheap to log: one line at Info, then Debug only, so a page
// that keeps knocking does not grow the log.
func TestServeLogsRefusalsBriefly(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	log := &logBuf{}
	addr := serveLog(t, p, time.Hour, log)
	for range 3 {
		dialAs(t, addr, "", "http://127.0.0.1:8000")
	}
	l := log.String()
	if n := strings.Count(l, `level=INFO msg="refused a connection"`); n != 1 {
		t.Errorf("%d refusals at Info, want 1:\n%s", n, l)
	}
	if n := strings.Count(l, "level=WARN"); n != 0 {
		t.Errorf("%d warnings:\n%s", n, l)
	}
	if n := strings.Count(l, `level=DEBUG msg="refused a connection"`); n != 3 {
		t.Errorf("%d refusals at Debug, want 3:\n%s", n, l)
	}
}

// A connection that passes the handshake checks but never proves itself is
// logged like a refused one: at Debug, and at Info at most once a minute,
// with a fixed reason. Nothing the peer sent, such as a header or a
// database id, reaches a line above Debug.
func TestServeLogsUnprovenConnectionsBriefly(t *testing.T) {
	host.SetHelloWait(t, 100*time.Millisecond)
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	log := &logBuf{}
	addr := serveLog(t, p, time.Hour, log)
	peer := strings.Repeat("peerwords", 100)
	for _, m := range []host.Msg{
		{Type: "hello", DB: peer, Nonce: nonceE},                // no such database
		{Type: "hello", DB: "Not An Id " + peer, Nonce: nonceE}, // not an id
		{Type: "hello", DB: "a", Nonce: peer},                   // not a nonce
	} {
		c, err := dialAs(t, addr, "", testOrigin)
		if err != nil {
			t.Fatal(err)
		}
		send(t, c, m)
		if got := next(t, c); got.Type != "error" {
			t.Fatalf("got %+v, want an error", got)
		}
	}
	silent, err := dialAs(t, addr, "", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	silent.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, err := silent.Read(); err != nil {
			break
		}
	}
	gone, err := dialAs(t, addr, "", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	gone.Close(1000, "")
	// A malformed upgrade, with the peer's words in a header.
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	nc.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(nc, "GET / HTTP/1.1\r\nHost: "+addr+"\r\nOrigin: "+testOrigin+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: "+peer+"\r\n\r\n")
	if resp, err := http.ReadResponse(bufio.NewReader(nc), nil); err != nil || resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("bad upgrade: %v %v", resp, err)
	}
	const dropped = `msg="dropped a connection that did not prove itself"`
	eventually(t, "every connection to be dropped", func() bool {
		return strings.Count(log.String(), "level=DEBUG "+dropped) == 6
	})
	l := log.String()
	if n := strings.Count(l, "level=INFO "+dropped); n != 1 {
		t.Errorf("%d drops at Info, want 1:\n%s", n, l)
	}
	for line := range strings.Lines(l) {
		if strings.Contains(line, " level=DEBUG ") {
			continue
		}
		if strings.Contains(line, "level=WARN") || strings.Contains(line, "peerwords") || strings.Contains(line, `msg=disconnected`) {
			t.Errorf("a line above Debug for an unproven connection: %s", line)
		}
	}
}

// A keep-alive connection that only ever gets refused is closed once it
// has been idle for the hello wait, so no program can hold one for ever.
func TestServeClosesIdleKeepAliveConnections(t *testing.T) {
	host.SetHelloWait(t, 100*time.Millisecond)
	p := paths(t)
	addr := serve(t, p, time.Hour)
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	nc.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(nc, "GET / HTTP/1.1\r\nHost: "+addr+"\r\n\r\n")
	br := bufio.NewReader(nc)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got %v %v, want 403", resp, err)
	}
	io.Copy(io.Discard, resp.Body)
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("read %v, want the server to close the idle connection", err)
	}
}

// Each change to config.yaml that the server loads shows in the log once,
// with how many unions it has, however many profiles are connected.
func TestServeLogsEachConfigLoadOnce(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	bound(t, p, "b", testOrigin, tok2)
	writeConfig(t, p, "format: 1\nunions: {}\n")
	log := &logBuf{}
	addr := serveLog(t, p, time.Hour, log)
	_, port, _ := net.SplitHostPort(addr)
	var conns []*ws.Conn
	for id, tok := range map[string]string{"a": tok1, "b": tok2} {
		c, err := dialAs(t, addr, "", testOrigin)
		if err != nil {
			t.Fatal(err)
		}
		nonceS := challenged(t, c, port, id, tok)
		send(t, c, host.Msg{Type: "proof", Proof: mac(tok, "extension:"+nonceS)})
		send(t, c, host.Msg{Type: "tree", Nodes: fakebrowser.New().Tree()})
		if m := next(t, c); m.Type != "status" {
			t.Fatalf("%s: got %+v", id, m)
		}
		conns = append(conns, c)
	}
	loaded := func() []string {
		var out []string
		for line := range strings.Lines(log.String()) {
			if strings.Contains(line, `msg="loaded config.yaml"`) {
				out = append(out, line)
			}
		}
		return out
	}
	if l := loaded(); len(l) != 1 || !strings.Contains(l[0], "level=INFO") || !strings.Contains(l[0], "file="+p.ConfigFile()) || !strings.Contains(l[0], "unions=0") {
		t.Fatalf("after the first rounds: %q", l)
	}
	// A union of neither profile: each host runs a round and changes nothing.
	writeConfig(t, p, "format: 1\nunions:\n  work:\n    x: toolbar/Work\n    y: toolbar/Work\n")
	for _, c := range conns {
		if m := next(t, c); m.Type != "status" {
			t.Fatalf("got %+v", m)
		}
	}
	if l := loaded(); len(l) != 2 || !strings.Contains(l[1], "unions=1") {
		t.Fatalf("after the change: %q", l)
	}
}

// A database file that exists but cannot be read is the server's problem:
// the log has the details, and the unproven peer only db-missing.
func TestServeTellsAnUnprovenPeerNoFileDetails(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	bound(t, p, "broken", testOrigin, tok2)
	if err := os.WriteFile(p.ProfileFile("broken"), []byte("format: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &logBuf{}
	c, err := dialAs(t, serveLog(t, p, time.Hour, log), "", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	send(t, c, host.Msg{Type: "hello", DB: "broken", Nonce: nonceE})
	m := next(t, c)
	if m.Code != "db-missing" || strings.Contains(m.Message, p.Data) || strings.Contains(m.Message, "yaml") {
		t.Fatalf("got %+v", m)
	}
	eventually(t, "the details in the log", func() bool { return strings.Contains(log.String(), p.ProfileFile("broken")) })
}

// A token that is not 64 lowercase hex digits was not written by install;
// the server says so in the log and proves nothing with it.
func TestServeRefusesAMalformedToken(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, strings.ToUpper(tok1))
	log := &logBuf{}
	c, err := dialAs(t, serveLog(t, p, time.Hour, log), "", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	send(t, c, host.Msg{Type: "hello", DB: "a", Nonce: nonceE})
	if m := next(t, c); m.Type != "error" || m.Code != "bad-token" {
		t.Fatalf("got %+v", m)
	}
	eventually(t, "the log line", func() bool { return strings.Contains(log.String(), "malformed") })
}

// When the listener fails, Serve stops its hosts and returns the error,
// rather than wait for hosts nothing will stop.
func TestServeStopsItsHostsWhenTheListenerFails(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", testOrigin, tok1)
	bound(t, p, "b", testOrigin, tok2)
	writeConfig(t, p, workUnion)
	ln, err := host.Listen([]int{0})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- host.Serve(context.Background(), ln, host.Options{Paths: p, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Timeout: 300 * time.Millisecond, Poll: 20 * time.Millisecond, Ping: time.Hour})
	}()
	addr := ln.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	c, err := dialAs(t, addr, "", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	nonceS := challenged(t, c, port, "a", tok1)
	send(t, c, host.Msg{Type: "proof", Proof: mac(tok1, "extension:"+nonceS)})
	send(t, c, host.Msg{Type: "tree", Nodes: fakebrowser.New().Tree()})
	if m := next(t, c); m.Type != "apply" {
		t.Fatalf("got %+v", m)
	}
	ln.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve returned nil for a failed listener")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve still waits for its hosts")
	}
}
