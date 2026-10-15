package host_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
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

const origin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"

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
	ln, err := host.Listen([]int{0})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- host.Serve(ctx, ln, host.Options{Paths: p, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
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
	bound(t, p, "a", origin, "t1")
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	for _, c := range []struct{ name, host, origin string }{
		{"another host name", "localhost:" + port, origin},
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
	bound(t, p, "a", origin, "t1")
	bound(t, p, "untokened", origin, "")
	bound(t, p, "other", "chrome-extension://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "t3")
	addr := serve(t, p, time.Hour)
	for _, c := range []struct{ id, nonce, code string }{
		{"missing", nonceE, "db-missing"},
		{"untokened", nonceE, "bad-token"},
		{"other", nonceE, "bad-token"},
		{"a", "short", "bad-token"},
	} {
		conn, err := dialAs(t, addr, "", origin)
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
	bound(t, p, "a", origin, "t1")
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	c, err := dialAs(t, addr, "", origin)
	if err != nil {
		t.Fatal(err)
	}
	send(t, c, host.Msg{Type: "proof", Proof: strings.Repeat("0", 64)})
	challenged(t, c, port, "a", "t1")
}

func TestServeRefusesAWrongProof(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", origin, "t1")
	bound(t, p, "b", origin, "t2")
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	refused := func(name string, proof func(nonceS string) string) {
		t.Helper()
		c, err := dialAs(t, addr, "", origin)
		if err != nil {
			t.Fatal(err)
		}
		nonceS := challenged(t, c, port, "a", "t1")
		send(t, c, host.Msg{Type: "proof", Proof: proof(nonceS)})
		if m := next(t, c); m.Type != "error" || m.Code != "bad-token" {
			t.Errorf("%s: got %+v", name, m)
		}
	}
	since := time.Now()
	refused("no proof", func(string) string { return "" })
	refused("another database's token", func(n string) string { return mac("t2", "extension:"+n) })
	refused("the server's label", func(n string) string { return mac("t1", "server:"+port+":"+n) })
	if db.Connected(p, "a", since) {
		t.Fatal("a refused proof counted as reporting in")
	}

	// A proof that was right once is refused against a new nonce.
	c, _ := dialAs(t, addr, "", origin)
	old := mac("t1", "extension:"+challenged(t, c, port, "a", "t1"))
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
	bound(t, p, "a", origin, "t1")
	bound(t, p, "b", origin, "t2")
	writeConfig(t, p, workUnion)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	tokens := map[string]string{"a": "t1", "b": "t2"}
	since := time.Now()
	for _, id := range []string{"a", "b"} {
		c, err := dialAs(t, addr, "", origin)
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
	c, _ := dialAs(t, addr, "", origin)
	nonceS := challenged(t, c, port, "a", "t1")
	send(t, c, host.Msg{Type: "proof", Proof: mac("t1", "extension:"+nonceS)})
	if m := next(t, c); m.Code != "db-locked" {
		t.Fatalf("second connection for a: %+v", m)
	}
}

func TestServePings(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", origin, "t1")
	c, err := dialAs(t, serve(t, p, 20*time.Millisecond), "", origin)
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
	bound(t, p, "a", origin, "t1")
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	silent, err := dialAs(t, addr, "", origin)
	if err != nil {
		t.Fatal(err)
	}
	// One that says hello but never answers the challenge is dropped too.
	halfway, err := dialAs(t, addr, "", origin)
	if err != nil {
		t.Fatal(err)
	}
	challenged(t, halfway, port, "a", "t1")
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
	bound(t, p, "a", origin, "t1")
	bound(t, p, "b", origin, "t2")
	writeConfig(t, p, workUnion)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	c, err := dialAs(t, addr, "", origin)
	if err != nil {
		t.Fatal(err)
	}
	nonceS := challenged(t, c, port, "a", "t1")
	send(t, c, host.Msg{Type: "proof", Proof: mac("t1", "extension:"+nonceS)})
	time.Sleep(300 * time.Millisecond)
	send(t, c, host.Msg{Type: "tree", Nodes: fakebrowser.New().Tree()})
	if m := next(t, c); m.Type != "apply" {
		t.Fatalf("got %+v", m)
	}
}
