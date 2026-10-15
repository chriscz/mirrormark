# mirrormark over a local server: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. When writing Go, use the `use-modern-go` skill.

**Goal:** The extension talks to one long-running `mirrormark serve` over a WebSocket on `127.0.0.1` instead of native messaging. `install` runs the server as a systemd user service and hands each profile a token. Native messaging is then removed, and the pending Brave and LibreWolf end-to-end tests run on the new transport.

**Architecture:** `host.Run` keeps all of today's per-connection logic, but it reads and writes messages through a `host.Conn` instead of stdin and stdout. A small standard-library package, `internal/ws`, speaks WebSocket. `host.Serve` accepts connections, checks `Host` and `Origin`, and runs one `host.Run` per connection. The hello starts a challenge: the host proves it holds the profile's token, then the extension proves it does, so the token never crosses the socket. `install` writes the origin and token into the profile block before it opens the bind page. The extension switches transport in one commit, together with the protocol bump and the e2e harness. Native messaging goes in the next commit.

**Tech Stack:** Go 1.27.2 (standard library plus `go.yaml.in/yaml/v3`; no new dependency); plain JavaScript extension; systemd user units; e2e over the DevTools pipe (Chrome, Brave) and WebDriver BiDi (Developer Edition, LibreWolf) in the dev image.

**Spec:** [`../0001_local-bookmark-sync-host.md`](../0001_local-bookmark-sync-host.md), Section 11 and Phase 8, with its edits to Sections 1, 2, 5–9. Findings: [`localhost-transport-spike.md`](localhost-transport-spike.md).

**Where this goes:** after [`plan-profile-selection.md`](plan-profile-selection.md) (done). It replaces Tasks 7–8 of [`plan-brave-librewolf.md`](plan-brave-librewolf.md), which had not run: they are Tasks 8 and 9 here, on the new transport, and that plan points here.

## Global Constraints

- Everything in the earlier plans' Global Constraints still holds: `bin/dev run make` only; no `any` values or unchecked type assertions; no RFC numbers, section references or `docs/rfcs/` links in code, comments, tests or build files; README updated in the same commit; every commit carries `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- **Every commit** passes `bin/dev run make` and `bin/dev run make check-e2e`.
- **No prefix leaves users without a transport.** Tasks 1–5 change nothing the extension uses: native messaging works throughout. Task 6 switches the extension, the protocol, `install`'s bind check and the e2e harness in **one commit**, because none of them can land without the others. Task 7 removes native messaging only after that.
- **Ports, exactly:** `[61959, 63018, 63454]`, in `extension/ports.json`, read by `extension.Ports()` and by `background.js`. Nowhere else in production code.
- **Bind on `127.0.0.1` only.** No other address, no `localhost`.
- **Limits:** messages to the extension ≤ `maxOut` (1 MB), from it ≤ `maxIn` (64 MB), as today. Through the server, ≤ `helloMax` (4 KB) until the proof.
- **Ping:** `{"v": <Version>, "type": "ping"}` every 20 s per connection (`Options.Ping`, default `20 * time.Second`).
- **Protocol version** stays 1. The new extension only dials the server and the old one only uses native messaging, so no peer of one ever meets the other and a bump would never be checked.
- **Profile block fields:** `origin` (`chrome-extension://<id>` or `moz-extension://<uuid>`, no trailing slash) and `token` (64 lowercase hex digits from 32 bytes of `crypto/rand`), both `omitempty`.
- **Bind URL fragment:** `#<db>.<token>`.
- **Error code** for a wrong origin, a database with no token, a malformed nonce or a wrong proof: `bad-token`.
- **The challenge, exactly.** Extension → host `{"v", "type": "hello", "db", "nonce": nonce_e, "browser"}`; host → extension `{"v", "type": "challenge", "nonce": nonce_s, "proof": proof_s}`, sent only when the database exists, its `origin` is the connection's and it has a `token`; extension → host `{"v", "type": "proof", "proof": proof_e}`, sent only after the extension checked `proof_s`. `proof_s = hex(HMAC-SHA256(key, "server:" + port + ":" + nonce_e))` and `proof_e = hex(HMAC-SHA256(key, "extension:" + nonce_s))`, where `key` is the token's 64 hex digits as ASCII bytes (never hex-decoded), `port` is the listening port in decimal, and nonces are 32 bytes of `crypto/rand` (`crypto.getRandomValues` in the extension) as 64 lowercase hex digits. Hex is always lowercase. The host compares with `hmac.Equal`; the extension with `!==`, since each attempt has a new nonce. The host takes the database's lock and touches the lock file only after a right `proof_e`. The token is never sent on the socket.
- **Hello wait:** a connection has `helloWait` (10 s) from the upgrade to a right `proof_e`. Until then its socket has a read deadline; when it runs out the server closes the connection without touching any file. The proof clears the deadline.
- **Unit path:** `filepath.Join(filepath.Dir(p.Config), "systemd", "user", "mirrormark.service")`, which is `$XDG_CONFIG_HOME/systemd/user/mirrormark.service`, or `~/.config/systemd/user/…` when that is unset. `ExecStart=<selfPath()> serve`, `Restart=on-failure`, `WantedBy=default.target`.
- **Not running, exactly:** the popup text `mirrormark is not running. Start it with \`systemctl --user start mirrormark\`, or run \`mirrormark serve\`.`
- **Log lines the e2e tests read:** `msg=listening`, `msg=connected … conn=<n> db=<id>`, `msg=disconnected … conn=<n>`.
- **Safety stays:** explicit `--profile`, one bind open per ready profile, the Gecko browser check before any write, databases before profile writes, per-browser `--purge`, and shared files kept while another browser uses them.

## File structure

```
extension/
  ports.json          the three ports (Task 3)
  embed.go            Ports() (Task 3); HostName goes (Task 7)
  bind.js             stores db and token (Task 4)
  background.js       WebSocket client (Task 6)
  manifest.json       CSP, no nativeMessaging, 0.2.0 (Task 6)
internal/ws/
  ws.go               Upgrade, Dial, Conn.Read/Write/Close/SetReadDeadline (Task 2)
  ws_test.go
internal/host/
  protocol.go         Conn, encode/decode, Msg.Nonce/Proof (Tasks 1, 3); Stream,
                      ReadMsg, WriteMsg go (Task 7)
  host.go             Run(ctx, Conn, Options); the challenge (Tasks 1, 3)
  server.go           Listen, Serve, admit, wsConn, helloWait (Task 3)
  server_test.go
internal/db/
  types.go            Profile.Origin, Profile.Token (Task 3)
  paths.go            ServeLock (Task 3)
  bind.go             Bind (Task 4)
cmd/mirrormark/
  main.go             serve (Task 3); host mode goes (Task 7)
  service.go          unit, startServer, serverRunning, stopServer,
                      serviceUsers (Tasks 5, 6)
  install.go          token hand-off (Task 4), service (Task 5), bind check
                      (Task 6), old host manifests (Task 7)
internal/chrome/      Bind takes a token (Task 4); one system file (Task 7)
internal/firefox/     Bind takes a token (Task 4); no host manifest (Task 7)
e2e/                  harness starts serve (Task 6); Brave and LibreWolf (Tasks 8, 9)
```

## Interfaces between tasks

```go
// internal/host (Task 1)
type Conn interface {
	Read() (Msg, error) // the next message; io.EOF once the extension has gone
	Write(Msg) error
}
func Run(ctx context.Context, c Conn, o Options) error
func Stream(r io.Reader, w io.Writer) Conn // native messaging framing; gone in Task 7
func encode(m Msg) ([]byte, error)         // JSON, refuses over maxOut
func decode(b []byte) (Msg, error)

// internal/ws (Task 2)
func Upgrade(w http.ResponseWriter, r *http.Request, max int) (*Conn, error)
func Dial(addr string, h http.Header, max int) (*Conn, error) // client side, for tests
func (c *Conn) Read() ([]byte, error)                         // io.EOF after the peer's close; a local Close wraps net.ErrClosed
func (c *Conn) Write(msg []byte) error                        // safe from several goroutines
func (c *Conn) Close(code uint16, reason string) error        // idempotent; waits at most 1 s for a write
func (c *Conn) SetReadDeadline(t time.Time) error             // os.ErrDeadlineExceeded; zero time clears it
func (c *Conn) SetReadLimit(max int)                          // Task 3; between Reads only
// A message's buffer grows with the bytes received, not with the length a
// header claims. Close frames are validated (code range, UTF-8 reason); a
// handshake for another version gets 426.

// internal/host (Task 3)
type Options struct {
	Paths   db.Paths
	Log     *slog.Logger
	Timeout time.Duration
	Poll    time.Duration
	Ping    time.Duration // Serve: how often to ping each connection; default 20 s
	Origin  string        // Run: the connection's Origin; set by Serve
	Port    string        // Run: the port the server listens on, which the host's proof names; set by Serve
}
func Listen(ports []int) (net.Listener, error)
func Serve(ctx context.Context, ln net.Listener, o Options) error

// extension (Task 3)
func Ports() ([]int, error)

// internal/db (Tasks 3, 4)
func (p Paths) ServeLock() string
func Bind(p Paths, id, origin string) (token string, err error)

// internal/chrome, internal/firefox (Task 4)
func (b chrome.Browser) Bind(bin, userDataDir, profileDir, extID, dbID, token string, reported func(time.Time) bool) error
func (b firefox.Browser) Bind(bin, profileDir, uuid, dbID, token string, reported func(time.Time) bool) error

// cmd/mirrormark (Tasks 5, 6)
var runSystemctl func(args ...string) error
func startServer(p db.Paths, self string, stdout io.Writer) error
func stopServer(p db.Paths, users []string, stdout io.Writer) error
func serviceUsers(p db.Paths, browser string, stderr io.Writer) []string
func serverRunning(p db.Paths) bool // Task 6
```

## Tasks

| # | Commit subject |
| --- | --- |
| 1 | `refactor(rfc-0001): run the host over a message connection` |
| 2 | `feat(rfc-0001): a standard-library WebSocket server` |
| 3 | `feat(rfc-0001): serve extension connections on 127.0.0.1 [new feature]` |
| 4 | `feat(rfc-0001): hand each bound profile a token` |
| 5 | `feat(rfc-0001): run mirrormark serve as a systemd user service [new feature]` |
| 6 | `feat(rfc-0001): connect the extension to mirrormark serve` |
| 7 | `refactor(rfc-0001): remove native messaging` |
| 8 | `test(rfc-0001): install and bind Brave and LibreWolf end to end` |
| 9 | `test(rfc-0001): run the sync tests between Brave and LibreWolf` |

---

### Task 1: The host runs over a message connection

**Files:**
- Modify: `internal/host/protocol.go`, `internal/host/host.go`, `cmd/mirrormark/main.go:91`, `internal/host/host_test.go` (lines 88–104 and 362–366)

**Interfaces:**
- Produces: `host.Conn`, `host.Stream`, `host.Run(ctx, Conn, Options)`, unexported `encode`/`decode` (used by Task 3's `wsConn`).

No behaviour changes. The existing host tests are the test.

- [ ] **Step 1: `protocol.go`.** Split the JSON and size checks out of `ReadMsg` and `WriteMsg`, and add the connection type:

```go
// Conn carries whole messages between the host and one extension.
type Conn interface {
	// Read returns the next message, or io.EOF once the extension has gone.
	Read() (Msg, error)
	// Write sends one message.
	Write(Msg) error
}

// Stream is a Conn in native messaging's framing: each message is its
// length as a native-endian uint32, then that many bytes of JSON.
func Stream(r io.Reader, w io.Writer) Conn { return stream{r, w} }

type stream struct {
	r io.Reader
	w io.Writer
}

func (s stream) Read() (Msg, error)  { return ReadMsg(s.r) }
func (s stream) Write(m Msg) error { return WriteMsg(s.w, m) }

// encode is m as JSON, refused when the browsers could not take it.
func encode(m Msg) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(b) > maxOut {
		return nil, fmt.Errorf("message of %d bytes is over the browsers' 1 MB limit", len(b))
	}
	return b, nil
}

func decode(b []byte) (Msg, error) {
	var m Msg
	if err := json.Unmarshal(b, &m); err != nil {
		return Msg{}, fmt.Errorf("bad message: %w", err)
	}
	return m, nil
}
```

`ReadMsg` ends with `return decode(buf)`. `WriteMsg` starts with `b, err := encode(m); if err != nil { return err }` and keeps its single write.

- [ ] **Step 2: `host.go`.** `func Run(ctx context.Context, c Conn, o Options) error`. The reader goroutine calls `c.Read()` instead of `ReadMsg(r)`. `state.out io.Writer` becomes `state.conn Conn`, and `send` calls `s.conn.Write(m)`. Remove the `io` import only if nothing else uses it (`await` still returns `io.EOF`).

- [ ] **Step 3: Callers.** `main.go`: `host.Run(context.Background(), host.Stream(stdin, stdout), host.Options{…})`. In `host_test.go`, `host.Run(ctx, host.Stream(hr, hw), ho)` in `start`, and `host.Run(context.Background(), host.Stream(hr, hw), …)` in `TestOtherVersionIsRefused`.

- [ ] **Step 4: Run** `bin/dev run make` → PASS, then `bin/dev run make check-e2e` → PASS.

- [ ] **Step 5: Commit** `refactor(rfc-0001): run the host over a message connection`.

---

### Task 2: A standard-library WebSocket server

**Files:**
- Create: `internal/ws/ws.go`, `internal/ws/ws_test.go`

**Interfaces:**
- Produces: `ws.Upgrade`, `ws.Dial`, `(*ws.Conn).Read`, `Write`, `Close`, `SetReadDeadline`.

- [ ] **Step 1: Write the failing tests** in `internal/ws/ws_test.go`:

```go
package ws_test

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chriscz/mirrormark/internal/ws"
)

// serve runs handle on each upgraded connection and sends what it returns
// to got.
func serve(t *testing.T, max int, handle func(*ws.Conn) error) (addr string, got chan error) {
	got = make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Upgrade(w, r, max)
		if err != nil {
			got <- err
			return
		}
		got <- handle(c)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), got
}

// echo serves one connection: each text message comes back as it came.
// got receives Read's final error.
func echo(t *testing.T, max int) (addr string, got chan error) {
	return serve(t, max, func(c *ws.Conn) error {
		for {
			m, err := c.Read()
			if err != nil {
				return err
			}
			c.Write(m)
		}
	})
}

// wait returns what the server sent to got, failing the test if nothing
// comes.
func wait(t *testing.T, got chan error) error {
	t.Helper()
	select {
	case err := <-got:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not finish")
		return nil
	}
}

func dial(t *testing.T, addr string) *ws.Conn {
	t.Helper()
	c, err := ws.Dial(addr, http.Header{"Origin": {"chrome-extension://x"}}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEchoesTextMessages(t *testing.T) {
	addr, _ := echo(t, 1<<20)
	c := dial(t, addr)
	for _, msg := range []string{"", "hello", strings.Repeat("x", 200), strings.Repeat("y", 70000)} {
		if err := c.Write([]byte(msg)); err != nil {
			t.Fatal(err)
		}
		got, err := c.Read()
		if err != nil || string(got) != msg {
			t.Fatalf("sent %d bytes, got %d, %v", len(msg), len(got), err)
		}
	}
}

func TestCloseEndsRead(t *testing.T) {
	addr, got := echo(t, 1<<20)
	c := dial(t, addr)
	c.Close(1000, "bye")
	if err := wait(t, got); err != io.EOF {
		t.Fatalf("server Read after close: %v", err)
	}
}

// handshake sends a hand-made opening handshake with the given key and
// version and returns the answer.
func handshake(t *testing.T, nc net.Conn, br *bufio.Reader, addr, key, version string) *http.Response {
	t.Helper()
	io.WriteString(nc, "GET / HTTP/1.1\r\nHost: "+addr+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: "+key+"\r\nSec-WebSocket-Version: "+version+"\r\n\r\n")
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func connect(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	nc.SetDeadline(time.Now().Add(5 * time.Second)) // fail, not hang, if no close frame comes
	return nc, bufio.NewReader(nc)
}

// raw opens a connection and does the handshake by hand, for frames Dial
// would never send.
func raw(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	nc, br := connect(t, addr)
	resp := handshake(t, nc, br, addr, "dGhlIHNhbXBsZSBub25jZQ==", "13")
	if resp.StatusCode != 101 {
		t.Fatalf("handshake: %v", resp.Status)
	}
	// The example key and answer from the WebSocket specification.
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept %q", got)
	}
	return nc, br
}

// encode makes one frame with the given first byte and the shortest
// length encoding, masked unless mask is false.
func encode(b0 byte, payload []byte, mask bool) []byte {
	bit := byte(0)
	if mask {
		bit = 0x80
	}
	h := []byte{b0}
	switch n := len(payload); {
	case n < 126:
		h = append(h, bit|byte(n))
	case n <= 0xffff:
		h = binary.BigEndian.AppendUint16(append(h, bit|126), uint16(n))
	default:
		h = binary.BigEndian.AppendUint64(append(h, bit|127), uint64(n))
	}
	if mask {
		h = append(h, 1, 2, 3, 4)
		p := make([]byte, len(payload))
		for i := range payload {
			p[i] = payload[i] ^ []byte{1, 2, 3, 4}[i%4]
		}
		payload = p
	}
	return append(h, payload...)
}

// frame writes one frame made by encode.
func frame(nc net.Conn, b0 byte, payload []byte, mask bool) {
	nc.Write(encode(b0, payload, mask))
}

// readFrame reads one unmasked frame from the server.
func readFrame(t *testing.T, br *bufio.Reader) (b0 byte, payload []byte) {
	t.Helper()
	var h [2]byte
	if _, err := io.ReadFull(br, h[:]); err != nil {
		t.Fatal(err)
	}
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(br, b[:]); err != nil {
			t.Fatal(err)
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(br, b[:]); err != nil {
			t.Fatal(err)
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	payload = make([]byte, n)
	if _, err := io.ReadFull(br, payload); err != nil {
		t.Fatal(err)
	}
	return h[0], payload
}

// closeFrame reads frames until the server's close frame and returns its
// code and reason. A close frame with no payload has no code: 1005.
func closeFrame(t *testing.T, br *bufio.Reader) (uint16, string) {
	t.Helper()
	for {
		b0, p := readFrame(t, br)
		if b0&0x0f != 0x8 {
			continue
		}
		if len(p) < 2 {
			return 1005, ""
		}
		return binary.BigEndian.Uint16(p), string(p[2:])
	}
}

func closeCode(t *testing.T, br *bufio.Reader) uint16 {
	t.Helper()
	code, _ := closeFrame(t, br)
	return code
}

func TestRefusesWhatABrowserNeverSends(t *testing.T) {
	for _, c := range []struct {
		name string
		b0   byte
		mask bool
		want uint16
	}{
		{"binary", 0x82, true, 1003},
		{"unmasked", 0x81, false, 1002},
		{"reserved bits", 0xc1, true, 1002},
		{"continuation first", 0x80, true, 1002},
	} {
		t.Run(c.name, func(t *testing.T) {
			addr, got := echo(t, 1<<20)
			nc, br := raw(t, addr)
			frame(nc, c.b0, []byte("x"), c.mask)
			if code := closeCode(t, br); code != c.want {
				t.Errorf("close code %d, want %d", code, c.want)
			}
			if err := wait(t, got); err == nil || err == io.EOF {
				t.Errorf("server Read: %v", err)
			}
		})
	}
}

func TestRefusesAMessageOverTheLimit(t *testing.T) {
	addr, _ := echo(t, 10)
	nc, br := raw(t, addr)
	frame(nc, 0x01, []byte("123456"), true) // text, not final
	frame(nc, 0x80, []byte("789012"), true) // continuation, final: 12 bytes
	if code := closeCode(t, br); code != 1009 {
		t.Fatalf("close code %d", code)
	}
}

func TestJoinsFragmentsAndAnswersPings(t *testing.T) {
	addr, _ := echo(t, 1<<20)
	nc, br := raw(t, addr)
	frame(nc, 0x01, []byte("ab"), true)
	frame(nc, 0x89, []byte("p"), true) // a ping between fragments
	frame(nc, 0x80, []byte("cd"), true)
	var got []string
	for len(got) < 2 {
		b0, p := readFrame(t, br)
		got = append(got, string([]byte{b0})+string(p))
	}
	if got[0] != "\x8ap" || got[1] != "\x81abcd" {
		t.Fatalf("got %q, want the pong, then the joined message", got)
	}
}

// Messages whose frames use the 16-bit and 64-bit length encodings, whole
// and in fragments, come back intact.
func TestReadsLongLengths(t *testing.T) {
	long16, long64 := bytes.Repeat([]byte("a"), 300), bytes.Repeat([]byte("b"), 70000)
	for _, c := range []struct {
		name   string
		frames [][]byte
		want   []byte
	}{
		{"16-bit", [][]byte{encode(0x81, long16, true)}, long16},
		{"64-bit", [][]byte{encode(0x81, long64, true)}, long64},
		{"16-bit then 64-bit fragments", [][]byte{encode(0x01, long16, true), encode(0x80, long64, true)},
			append(append([]byte{}, long16...), long64...)},
	} {
		t.Run(c.name, func(t *testing.T) {
			addr, _ := echo(t, 1<<20)
			nc, br := raw(t, addr)
			for _, f := range c.frames {
				nc.Write(f)
			}
			b0, p := readFrame(t, br)
			if b0 != 0x81 || !bytes.Equal(p, c.want) {
				t.Fatalf("got %#x with %d bytes, want %d", b0, len(p), len(c.want))
			}
		})
	}
}

// A frame of exactly the limit that trickles in, in small pieces, is
// still read whole.
func TestReadsAFrameThatArrivesInPieces(t *testing.T) {
	const max = 70000
	addr, _ := echo(t, max)
	nc, br := raw(t, addr)
	msg := bytes.Repeat([]byte("q"), max)
	f := encode(0x81, msg, true)
	for len(f) > 0 {
		n := min(len(f), 1000)
		if _, err := nc.Write(f[:n]); err != nil {
			t.Fatal(err)
		}
		f = f[n:]
		time.Sleep(time.Millisecond)
	}
	b0, p := readFrame(t, br)
	if b0 != 0x81 || !bytes.Equal(p, msg) {
		t.Fatalf("got %#x with %d bytes", b0, len(p))
	}
}

func TestRefusesAPlainRequest(t *testing.T) {
	addr, _ := echo(t, 1<<20)
	resp, err := http.Get("http://" + addr + "/")
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("%v %v", resp, err)
	}
}

func TestRefusesABadHandshake(t *testing.T) {
	for _, c := range []struct {
		name, key, version string
		want               int
	}{
		{"key not base64", "not base64!", "13", http.StatusBadRequest},
		{"key not 16 bytes", "dGhlIHNhbXBsZQ==", "13", http.StatusBadRequest},
		{"old version", "dGhlIHNhbXBsZSBub25jZQ==", "8", http.StatusUpgradeRequired},
	} {
		t.Run(c.name, func(t *testing.T) {
			addr, got := echo(t, 1<<20)
			nc, br := connect(t, addr)
			resp := handshake(t, nc, br, addr, c.key, c.version)
			nc.Close() // so a wrongly upgraded connection ends with io.EOF
			if resp.StatusCode != c.want {
				t.Errorf("status %s, want %d", resp.Status, c.want)
			}
			if c.want == http.StatusUpgradeRequired && resp.Header.Get("Sec-WebSocket-Version") != "13" {
				t.Errorf("Sec-WebSocket-Version %q", resp.Header.Get("Sec-WebSocket-Version"))
			}
			if err := wait(t, got); err == nil || err == io.EOF {
				t.Errorf("Upgrade succeeded: %v", err)
			}
		})
	}
}

// More frames a browser never sends: each is refused with the right code
// before the server reads, or waits for, any payload.
func TestRefusesMalformedFrames(t *testing.T) {
	for _, c := range []struct {
		name string
		raw  []byte
		want uint16
	}{
		// A 64-bit length far over the limit, with no payload behind it.
		{"huge length", []byte{0x81, 0xff, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, 1009},
		{"top bit of a 64-bit length", []byte{0x81, 0xff, 0x80, 0, 0, 0, 0, 0, 0, 0}, 1009},
		{"16-bit length just over the limit", []byte{0x81, 0xfe, 0x00, 0x65}, 1009},
		{"64-bit length just over the limit", []byte{0x81, 0xff, 0, 0, 0, 0, 0, 0, 0, 0x65}, 1009},
		{"long ping", []byte{0x89, 0xfe, 0x00, 0x7e}, 1002},
		{"fragmented ping", []byte{0x09, 0x80, 1, 2, 3, 4}, 1002},
		{"text inside a fragmented text", []byte{0x01, 0x81, 0, 0, 0, 0, 'a', 0x81, 0x81, 0, 0, 0, 0, 'b'}, 1002},
		{"unknown opcode", []byte{0x83, 0x80, 1, 2, 3, 4}, 1003},
		{"reserved control opcode", []byte{0x8b, 0x80, 1, 2, 3, 4}, 1002},
		{"last reserved control opcode", []byte{0x8f, 0x80, 1, 2, 3, 4}, 1002},
		{"bad UTF-8", []byte{0x81, 0x82, 0, 0, 0, 0, 0xc3, 0x28}, 1007},
		{"close with one byte", encode(0x88, []byte{3}, true), 1002},
		{"close code 999", encode(0x88, []byte{0x03, 0xe7}, true), 1002},
		{"close code 1004", encode(0x88, []byte{0x03, 0xec}, true), 1002},
		{"close code 1005", encode(0x88, []byte{0x03, 0xed}, true), 1002},
		{"close code 1006", encode(0x88, []byte{0x03, 0xee}, true), 1002},
		{"close code 1015", encode(0x88, []byte{0x03, 0xf7}, true), 1002},
		{"close code 2999", encode(0x88, []byte{0x0b, 0xb7}, true), 1002},
		{"close code 5000", encode(0x88, []byte{0x13, 0x88}, true), 1002},
		{"close reason not UTF-8", encode(0x88, []byte{0x03, 0xe8, 0xc3, 0x28}, true), 1007},
	} {
		t.Run(c.name, func(t *testing.T) {
			addr, got := echo(t, 100)
			nc, br := raw(t, addr)
			nc.Write(c.raw)
			if code := closeCode(t, br); code != c.want {
				t.Errorf("close code %d, want %d", code, c.want)
			}
			if err := wait(t, got); err == nil || err == io.EOF {
				t.Errorf("server Read: %v", err)
			}
		})
	}
}

// A well-formed close is answered with 1000 and ends Read with io.EOF.
func TestAnswersAGoodClose(t *testing.T) {
	for _, c := range []struct {
		name    string
		payload []byte
	}{
		{"no payload", nil},
		{"1000", []byte{0x03, 0xe8}},
		{"1001 with a reason", []byte{0x03, 0xe9, 'b', 'y', 'e'}},
		{"1014", []byte{0x03, 0xf6}},
		{"3000", []byte{0x0b, 0xb8}},
		{"4999", []byte{0x13, 0x87}},
	} {
		t.Run(c.name, func(t *testing.T) {
			addr, got := echo(t, 100)
			nc, br := raw(t, addr)
			frame(nc, 0x88, c.payload, true)
			if code := closeCode(t, br); code != 1000 {
				t.Errorf("close code %d, want 1000", code)
			}
			if err := wait(t, got); err != io.EOF {
				t.Errorf("server Read: %v", err)
			}
		})
	}
}

func TestReadEndsWithEOFWhenThePeerHangsUp(t *testing.T) {
	addr, got := echo(t, 1<<20)
	nc, _ := raw(t, addr)
	nc.Close()
	if err := wait(t, got); err != io.EOF {
		t.Fatalf("server Read: %v", err)
	}
}

func TestHangingUpInsideAMessageIsUnexpected(t *testing.T) {
	addr, got := echo(t, 1<<20)
	nc, _ := raw(t, addr)
	frame(nc, 0x01, []byte("ab"), true) // text, not final
	nc.Close()
	if err := wait(t, got); err != io.ErrUnexpectedEOF {
		t.Fatalf("server Read: %v", err)
	}
}

func TestReadDeadline(t *testing.T) {
	addr, got := serve(t, 100, func(c *ws.Conn) error {
		if err := c.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
			return err
		}
		_, err := c.Read()
		return err
	})
	raw(t, addr) // and send nothing
	select {
	case err := <-got:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("server Read: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Read did not time out")
	}
}

func TestDialRefusesAPlainServer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	if _, err := ws.Dial(strings.TrimPrefix(srv.URL, "http://"), nil, 1<<20); err == nil {
		t.Fatal("Dial succeeded against a server that does not upgrade")
	}
}

// The server pings from its own goroutine while the host writes replies,
// so writes from several goroutines must not interleave their frames.
func TestWritesFromManyGoroutines(t *testing.T) {
	addr, _ := echo(t, 1<<20)
	c := dial(t, addr)
	const writers, each = 8, 50
	msg := strings.Repeat("z", 300) // long enough for a 16-bit length
	errs := make(chan error, writers)
	for range writers {
		go func() {
			for range each {
				if err := c.Write([]byte(msg)); err != nil {
					errs <- err
					return
				}
			}
			errs <- nil
		}()
	}
	for range writers * each {
		got, err := c.Read()
		if err != nil || string(got) != msg {
			t.Fatalf("got %d bytes, %v", len(got), err)
		}
	}
	for range writers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestCloseTwice(t *testing.T) {
	addr, _ := echo(t, 1<<20)
	c := dial(t, addr)
	if err := c.Close(1000, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(1000, ""); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// A Write stuck on a peer that does not read must not hold Close up.
func TestCloseDoesNotWaitForAStuckWrite(t *testing.T) {
	closed := make(chan time.Duration, 1)
	addr, got := serve(t, 100, func(c *ws.Conn) error {
		werr := make(chan error, 1)
		go func() { werr <- c.Write(make([]byte, 64<<20)) }()
		time.Sleep(200 * time.Millisecond) // let the write fill the socket buffers
		start := time.Now()
		c.Close(1001, "")
		closed <- time.Since(start)
		return <-werr
	})
	raw(t, addr) // and never read
	select {
	case d := <-closed:
		if d > 3*time.Second {
			t.Fatalf("Close took %v", d)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Close stalled behind the write")
	}
	if err := wait(t, got); err == nil {
		t.Fatal("the stuck Write succeeded")
	}
}

func TestCloseTruncatesALongReason(t *testing.T) {
	reason := strings.Repeat("é", 100) // 200 bytes, two to a rune
	addr, _ := serve(t, 100, func(c *ws.Conn) error { return c.Close(1000, reason) })
	_, br := raw(t, addr)
	code, got := closeFrame(t, br)
	if code != 1000 || len(got) > 123 || len(got) < 122 || !utf8.ValidString(got) || !strings.HasPrefix(reason, got) {
		t.Fatalf("code %d, reason of %d bytes %q", code, len(got), got)
	}
}

// fakeConn is a connection that reads from r and swallows writes.
type fakeConn struct{ r io.Reader }

func (f fakeConn) Read(p []byte) (int, error)       { return f.r.Read(p) }
func (f fakeConn) Write(p []byte) (int, error)      { return len(p), nil }
func (f fakeConn) Close() error                     { return nil }
func (f fakeConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (f fakeConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (f fakeConn) SetDeadline(time.Time) error      { return nil }
func (f fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (f fakeConn) SetWriteDeadline(time.Time) error { return nil }

// hijacker hands Upgrade a fakeConn.
type hijacker struct {
	nc net.Conn
	h  http.Header
}

func (w *hijacker) Header() http.Header         { return w.h }
func (w *hijacker) Write(p []byte) (int, error) { return len(p), nil }
func (w *hijacker) WriteHeader(int)             {}
func (w *hijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.nc, bufio.NewReadWriter(bufio.NewReader(w.nc), bufio.NewWriter(w.nc)), nil
}

// fake upgrades a connection whose peer sends the bytes of r.
func fake(t testing.TB, r io.Reader, max int) *ws.Conn {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Sec-WebSocket-Version", "13")
	c, err := ws.Upgrade(&hijacker{nc: fakeConn{r}, h: http.Header{}}, req, max)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// allocated returns how many bytes f allocates.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A header that claims a frame of the full limit, then a little payload,
// commits memory for what arrived, not for what was claimed.
func TestAClaimedLengthDoesNotCommitMemory(t *testing.T) {
	const max = 64 << 20
	head := []byte{0x81, 0xff, 0, 0, 0, 0, 0x04, 0, 0, 0, 1, 2, 3, 4} // 64 MB, masked
	c := fake(t, io.MultiReader(bytes.NewReader(head), bytes.NewReader(make([]byte, 1000))), max)
	var err error
	n := allocated(func() { _, err = c.Read() })
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("Read: %v", err)
	}
	if n > 1<<20 {
		t.Fatalf("allocated %d bytes for 1000 bytes of payload", n)
	}
}

// FuzzRead feeds arbitrary bytes to a server connection: it must not
// panic, and must allocate in proportion to the bytes it was given, not to
// the lengths they claim.
func FuzzRead(f *testing.F) {
	for _, seed := range [][]byte{
		encode(0x81, []byte("hello"), true),
		append(encode(0x01, []byte("ab"), true), append(encode(0x89, []byte("p"), true), encode(0x80, []byte("cd"), true)...)...),
		encode(0x81, bytes.Repeat([]byte("x"), 300), true),
		encode(0x88, []byte{0x03, 0xe8, 'o', 'k'}, true),
		encode(0x88, []byte{3}, true),
		{0x81, 0xff, 0, 0, 0, 0, 0, 0, 0x04, 0, 1, 2, 3, 4},
		{0x81, 0xfe, 0x04, 0x00, 1, 2, 3, 4, 'a'},
		{0x8b, 0x80, 1, 2, 3, 4},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		c := fake(t, bytes.NewReader(data), 1024)
		n := allocated(func() {
			for range len(data) + 1 { // each Read takes at least one byte or fails
				if _, err := c.Read(); err != nil {
					return
				}
			}
			t.Error("Read did not stop at the end of the input")
		})
		if limit := uint64(64<<10 + 32*len(data)); n > limit {
			t.Fatalf("allocated %d bytes for %d bytes of input", n, len(data))
		}
	})
}
```

- [ ] **Step 2: Run** `bin/dev run go test ./internal/ws/` → FAIL (package does not exist).

- [ ] **Step 3: Write `internal/ws/ws.go`:**

```go
// Package ws is the part of the WebSocket protocol (version 13) that
// mirrormark's server needs to talk JSON with a browser extension: the
// opening handshake, text messages in either direction, and ping, pong and
// close. It has no subprotocols, extensions or compression. Dial is the
// client side, for tests.
package ws

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	opCont  = 0x0
	opText  = 0x1
	opClose = 0x8
	opPing  = 0x9
	opPong  = 0xa
)

// The key every handshake answer is derived with.
const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// writeWait bounds one write, so a peer that stops reading cannot hold a
// writer for ever.
const writeWait = 10 * time.Second

// closeWait bounds Close: after it the socket is closed even if the close
// frame has not gone out, which also fails a write stuck behind it.
const closeWait = time.Second

// chunk is the most a frame's buffer grows by before more of the frame has
// arrived, so a header that claims a large frame costs little until the
// bytes come.
const chunk = 4096

// Conn is one WebSocket connection. One goroutine may Read while others
// Write.
type Conn struct {
	c      net.Conn
	r      *bufio.Reader
	max    int
	client bool       // a client masks what it sends; a server must not
	mu     sync.Mutex // one frame at a time
	once   sync.Once  // closes c once
}

func accept(key string) string {
	sum := sha1.Sum([]byte(key + magic))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// has reports whether header name lists token, ignoring case.
func has(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for t := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// Upgrade answers the opening handshake in r and takes over the
// connection. Messages over max bytes are refused. A request that is not
// a handshake gets 400, and one for another protocol version 426.
func Upgrade(w http.ResponseWriter, r *http.Request, max int) (*Conn, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if r.Method != http.MethodGet || !has(r.Header, "Connection", "upgrade") || !has(r.Header, "Upgrade", "websocket") {
		http.Error(w, "not a WebSocket handshake", http.StatusBadRequest)
		return nil, errors.New("not a WebSocket handshake")
	}
	if v := r.Header.Get("Sec-WebSocket-Version"); v != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "only WebSocket version 13 is supported", http.StatusUpgradeRequired)
		return nil, fmt.Errorf("WebSocket version %q", v)
	}
	if k, err := base64.StdEncoding.DecodeString(key); err != nil || len(k) != 16 {
		http.Error(w, "a bad Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, errors.New("a bad Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot upgrade", http.StatusInternalServerError)
		return nil, errors.New("the response cannot be taken over")
	}
	nc, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	nc.SetDeadline(time.Time{}) // the HTTP server's deadlines do not apply any more
	if _, err := io.WriteString(nc, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+accept(key)+"\r\n\r\n"); err != nil {
		nc.Close()
		return nil, err
	}
	return &Conn{c: nc, r: rw.Reader, max: max}, nil
}

// Dial opens a client connection to ws://addr/ with header h added to the
// handshake.
func Dial(addr string, h http.Header, max int) (*Conn, error) {
	nc, err := net.DialTimeout("tcp", addr, writeWait)
	if err != nil {
		return nil, err
	}
	var k [16]byte
	rand.Read(k[:])
	key := base64.StdEncoding.EncodeToString(k[:])
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil) // cannot fail for this URL
	req.Header = h.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", key)
	req.Header.Set("Sec-WebSocket-Version", "13")
	if v := h.Get("Host"); v != "" {
		req.Host = v // Request.Write takes the Host from here, not from Header
	}
	nc.SetDeadline(time.Now().Add(writeWait))
	if err := req.Write(nc); err != nil {
		nc.Close()
		return nil, err
	}
	br := bufio.NewReader(nc)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		nc.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != accept(key) {
		nc.Close()
		return nil, fmt.Errorf("handshake refused: %s", resp.Status)
	}
	nc.SetDeadline(time.Time{})
	return &Conn{c: nc, r: br, max: max, client: true}, nil
}

// Read returns the next text message. It answers pings and the peer's
// close itself, and returns io.EOF after a close or when the connection
// ends between messages, and io.ErrUnexpectedEOF when it ends inside one.
func (c *Conn) Read() ([]byte, error) {
	var msg []byte
	inMsg := false
	for {
		fin, op, n, mask, err := c.header()
		if err == io.EOF && inMsg {
			err = io.ErrUnexpectedEOF
		}
		if err != nil {
			return nil, err
		}
		switch op {
		case opPing, opPong, opClose:
			var ctl [125]byte
			p, err := c.payload(ctl[:0], n, mask)
			if err != nil {
				return nil, err
			}
			switch op {
			case opPing:
				if err := c.write(opPong, p); err != nil {
					return nil, err
				}
			case opClose:
				if code, why := checkClose(p); code != 0 {
					return nil, c.fail(code, why)
				}
				c.Close(1000, "") // answer the close, then hang up
				return nil, io.EOF
			}
			continue
		case opText:
			if inMsg {
				return nil, c.fail(1002, "a new message inside a fragmented one")
			}
			inMsg = true
		case opCont:
			if !inMsg {
				return nil, c.fail(1002, "a continuation with no message")
			}
		default:
			if op > opClose {
				return nil, c.fail(1002, "a reserved control opcode")
			}
			return nil, c.fail(1003, "only text messages are accepted")
		}
		if len(msg)+n > c.max {
			return nil, c.fail(1009, "message too large")
		}
		if msg, err = c.payload(msg, n, mask); err != nil {
			return nil, err
		}
		if fin {
			if !utf8.Valid(msg) {
				return nil, c.fail(1007, "a text message that is not UTF-8")
			}
			return msg, nil
		}
	}
}

// checkClose checks the payload of the peer's close frame. It returns 0
// if the frame is well formed, or the code to fail with and why.
func checkClose(p []byte) (uint16, string) {
	if len(p) == 0 {
		return 0, ""
	}
	if len(p) == 1 {
		return 1002, "a close frame with half a code"
	}
	// Codes below 1000 are unused, 1004 is reserved, 1005, 1006 and 1015
	// must never be sent, 1016 to 2999 are unassigned and 5000 up are
	// not codes at all.
	switch code := binary.BigEndian.Uint16(p); {
	case code < 1000, code >= 1004 && code <= 1006, code >= 1015 && code <= 2999, code >= 5000:
		return 1002, fmt.Sprintf("close code %d", code)
	}
	if !utf8.Valid(p[2:]) {
		return 1007, "a close reason that is not UTF-8"
	}
	return 0, ""
}

// header reads one frame header. n is at most max, and at most 125 for a
// control frame. mask is nil for an unmasked frame.
func (c *Conn) header() (fin bool, op byte, n int, mask *[4]byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.r, h[:]); err != nil {
		return
	}
	fin, op = h[0]&0x80 != 0, h[0]&0x0f
	masked := h[1]&0x80 != 0
	switch {
	case h[0]&0x70 != 0:
		err = c.fail(1002, "reserved bits set")
		return
	case masked == c.client:
		err = c.fail(1002, "masking the wrong way round")
		return
	}
	size := uint64(h[1] & 0x7f)
	switch size {
	case 126:
		var b [2]byte
		if err = c.full(b[:]); err != nil {
			return
		}
		size = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if err = c.full(b[:]); err != nil {
			return
		}
		size = binary.BigEndian.Uint64(b[:])
	}
	if op >= opClose && (size > 125 || !fin) {
		err = c.fail(1002, "a control frame that is long or fragmented")
		return
	}
	if size > uint64(c.max) {
		err = c.fail(1009, "message too large")
		return
	}
	n = int(size)
	if masked {
		mask = new([4]byte)
		err = c.full(mask[:])
	}
	return
}

// full fills b from inside a frame, where the connection ending is
// io.ErrUnexpectedEOF.
func (c *Conn) full(b []byte) error {
	_, err := io.ReadFull(c.r, b)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

// payload reads n bytes of payload onto the end of buf and unmasks them.
// buf grows with the bytes that arrive, never by more than chunk or its
// own length ahead of them.
func (c *Conn) payload(buf []byte, n int, mask *[4]byte) ([]byte, error) {
	start, end := len(buf), len(buf)+n
	for len(buf) < end {
		if len(buf) == cap(buf) {
			buf = slices.Grow(buf, min(end-len(buf), max(chunk, len(buf))))
		}
		next := min(cap(buf), end)
		if err := c.full(buf[len(buf):next]); err != nil {
			return nil, err
		}
		buf = buf[:next]
	}
	if mask != nil {
		for i := start; i < end; i++ {
			buf[i] ^= mask[(i-start)%4]
		}
	}
	return buf, nil
}

// SetReadDeadline sets when a Read that is waiting gives up, with an
// error that matches os.ErrDeadlineExceeded. The zero time means never.
// A Read that gave up may have stopped inside a frame, so close the
// connection after one.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.c.SetReadDeadline(t) }

// Write sends msg as one text frame.
func (c *Conn) Write(msg []byte) error { return c.write(opText, msg) }

func (c *Conn) write(op byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := []byte{0x80 | op}
	bit := byte(0)
	if c.client {
		bit = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		h = append(h, bit|byte(n))
	case n <= 0xffff:
		h = binary.BigEndian.AppendUint16(append(h, bit|126), uint16(n))
	default:
		h = binary.BigEndian.AppendUint64(append(h, bit|127), uint64(n))
	}
	if c.client {
		var mask [4]byte
		rand.Read(mask[:])
		h = append(h, mask[:]...)
		p := make([]byte, len(payload))
		for i := range payload {
			p[i] = payload[i] ^ mask[i%4]
		}
		payload = p
	}
	c.c.SetWriteDeadline(time.Now().Add(writeWait))
	_, err := c.c.Write(append(h, payload...))
	return err
}

// Close sends a close frame with code and reason, then closes the
// connection. A reason over 123 bytes, the room a control frame leaves
// after the code, is cut short. Close waits at most closeWait for a
// write in progress; a second Close does nothing and returns nil.
func (c *Conn) Close(code uint16, reason string) error {
	if len(reason) > 123 {
		reason = reason[:123]
		for len(reason) > 0 && !utf8.ValidString(reason) {
			reason = reason[:len(reason)-1] // drop the cut rune
		}
	}
	t := time.AfterFunc(closeWait, func() { c.shut() })
	c.write(opClose, append(binary.BigEndian.AppendUint16(nil, code), reason...))
	t.Stop()
	return c.shut()
}

// shut closes the socket the first time and returns its error; later
// calls return nil.
func (c *Conn) shut() error {
	var err error
	c.once.Do(func() { err = c.c.Close() })
	return err
}

// fail closes the connection with code and returns why.
func (c *Conn) fail(code uint16, why string) error {
	c.Close(code, why)
	return fmt.Errorf("websocket: %s", why)
}
```

- [ ] **Step 4: Run** `bin/dev run go test -count=1 ./internal/ws/` → PASS, then `bin/dev run make` → PASS (`check-vet` included). `bin/dev run make check-race` once, since `Conn` is shared between goroutines → PASS.

- [ ] **Step 5: Commit** `feat(rfc-0001): a standard-library WebSocket server`.

Review then added `SetReadDeadline` and `fix(rfc-0001): grow WebSocket buffers with the bytes received, and tighten close handling`: a buffer grows with the bytes received, close frames are validated, a handshake for another version gets 426, and a second `Close` does nothing. The code above is the package with both.

---

### Task 3: `mirrormark serve`

**Files:**
- Create: `extension/ports.json`, `internal/host/server.go`, `internal/host/server_test.go`, `internal/host/server_internal_test.go`
- Modify: `extension/embed.go`, `extension/embed_test.go`, `internal/firefox/firefox_test.go`, `internal/db/types.go`, `internal/db/paths.go`, `internal/db/paths_test.go`, `internal/host/export_test.go`, `internal/host/protocol.go`, `internal/host/host.go`, `cmd/mirrormark/main.go`, `cmd/mirrormark/main_test.go`, `README.md`

**Interfaces:**
- Consumes: Task 1 (`Conn`, `encode`, `decode`), Task 2 (`ws`).
- Produces: `extension.Ports`, `db.Profile.Origin`/`Token`, `Paths.ServeLock`, `host.Msg.Nonce`/`Proof`, `host.Options.Ping`/`Origin`/`Port`, `host.Listen`, `host.Serve`; log messages `listening`, `connected`, `disconnected`.

No extension uses the server yet: nothing a user sees changes except the new command.

- [ ] **Step 1: The ports.** `extension/ports.json`:

```json
[61959, 63018, 63454]
```

In `embed.go`, add `ports.json` to the `//go:embed` line, and:

```go
// Ports are the ports the server may listen on, in the order it tries
// them, from ports.json, which the extension reads too.
func Ports() ([]int, error) {
	data, err := Files.ReadFile("ports.json")
	if err != nil {
		return nil, err
	}
	var ports []int
	err = json.Unmarshal(data, &ports)
	return ports, err
}
```

Test in `embed_test.go`:

```go
func TestPorts(t *testing.T) {
	got, err := Ports()
	if err != nil || !slices.Equal(got, []int{61959, 63018, 63454}) {
		t.Fatalf("Ports() = %v, %v", got, err)
	}
}
```

`ports.json` is now one of the extension's files, so `TestUnpackWritesFilesAndKey` and `internal/firefox`'s `TestXPIHoldsTheExtensionWithoutAKey` list it too.

- [ ] **Step 2: The profile block and the lock.** In `types.go`'s `Profile`:

```go
	Origin  string `yaml:"origin,omitempty"` // the extension's origin in this profile; written by install
	Token   string `yaml:"token,omitempty"`  // the secret the extension proves its binding with; written by install
```

In `paths.go`:

```go
// ServeLock is held by the running server, so one runs per user.
func (p Paths) ServeLock() string { return filepath.Join(p.Data, "serve.lock") }
```

- [ ] **Step 3: The hello starts a challenge.** `Msg` gains, after `DB`:

```go
	Nonce    string           `json:"nonce,omitempty"` // hello, challenge: 32 random bytes in hex
	Proof    string           `json:"proof,omitempty"` // challenge, proof: an HMAC in hex
```

In `host.go`, `state` gains `pending pending`, and `handle` gains `case "proof": return s.proof(m)`. `hello` keeps its first checks (`s.self`, now `s.self != "" || s.pending.db != ""`; `ValidID`; the `os.Stat`), then ends with:

```go
	// Through the server, a connection must prove which database it is
	// before the host runs: native messaging needed no proof, since the
	// browser started the host for its own extension.
	if s.o.Origin != "" {
		return s.challenge(m)
	}
	return s.accept(m.DB, m.Browser)
}
```

`accept(id, browser string) error` is the rest of today's `hello` unchanged: `TryLock`, the lock file's mtime, `s.self`, the `connected` log line. After `s.self` is set it ends the hello wait of a connection that has one:

```go
	// A connection that limits how long an unproven peer may take learns
	// that this one is through.
	if c, ok := s.conn.(interface{ proven() }); ok {
		c.proven()
	}
``` Then:

```go
// pending is a hello waiting for the extension's proof.
type pending struct{ db, browser, token, nonce string }

// hex32 is 32 random bytes in lowercase hex, as both sides write a nonce
// and install writes a token.
var hex32 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// challenge answers a hello through the server. The connection came from
// the database's origin, but any program can send any Origin, and in
// Chromium every profile has the same one. So each side proves it holds
// the token install handed this profile, without sending it: the host
// first, so the extension tells a program posing as mirrormark nothing.
// The port in the host's proof stops a program on another port relaying
// the hello here and passing back this host's proof.
func (s *state) challenge(m Msg) error {
	d, err := db.LoadDB(s.o.Paths.ProfileFile(m.DB))
	if err != nil {
		// The details are for the log: the peer has proven nothing yet.
		s.o.Log.Warn("cannot read the database file", "db", m.DB, "err", err)
		return s.fail("db-missing", fmt.Sprintf("cannot read the database file for %s; run mirrormark install", m.DB))
	}
	if d.Profile.Token != "" && !hex32.MatchString(d.Profile.Token) {
		s.o.Log.Warn("the token in the database file is malformed; install writes 64 lowercase hex digits", "db", m.DB)
	}
	if !hex32.MatchString(d.Profile.Token) || d.Profile.Origin != s.o.Origin || !hex32.MatchString(m.Nonce) {
		return s.fail("bad-token", fmt.Sprintf("this profile's binding to %s is out of date; run mirrormark install for this profile again", m.DB))
	}
	b := make([]byte, 32)
	rand.Read(b) // never fails
	s.pending = pending{db: m.DB, browser: m.Browser, token: d.Profile.Token, nonce: hex.EncodeToString(b)}
	return s.send(Msg{Type: "challenge", Nonce: s.pending.nonce, Proof: mac(d.Profile.Token, "server:"+s.o.Port+":"+m.Nonce)})
}

// proof checks the extension's answer to the challenge, and only then
// runs the host for its database.
func (s *state) proof(m Msg) error {
	p := s.pending
	if s.self != "" || p.db == "" {
		return nil // no challenge is waiting
	}
	if !hmac.Equal([]byte(m.Proof), []byte(mac(p.token, "extension:"+p.nonce))) {
		return s.fail("bad-token", fmt.Sprintf("this profile's binding to %s is out of date; run mirrormark install for this profile again", p.db))
	}
	return s.accept(p.db, p.browser)
}

// mac is HMAC-SHA256 of msg, keyed by the token as text, in lowercase hex.
func mac(token, msg string) string {
	h := hmac.New(sha256.New, []byte(token))
	h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil))
}
```

Imports: `crypto/hmac`, `crypto/rand`, `crypto/sha256`, `encoding/hex`, `regexp`. In `Options`:

```go
	Ping    time.Duration // Serve: how often to ping each connection; default 20 s
	Origin  string        // the connection's Origin; Serve sets it, and then the hello starts a challenge
	Port    string        // the port the server listens on, named in the host's proof; Serve sets it
```

Add `challenge` and `proof` to the message types and `bad-token` to the list of `error` codes in `Msg`'s comment.

- [ ] **Step 4: Write the failing server tests** in `internal/host/server_test.go`:

```go
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
	bound(t, p, "a", origin, tok1)
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
	bound(t, p, "a", origin, tok1)
	bound(t, p, "untokened", origin, "")
	bound(t, p, "other", "chrome-extension://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", tok3)
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
	bound(t, p, "a", origin, tok1)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	c, err := dialAs(t, addr, "", origin)
	if err != nil {
		t.Fatal(err)
	}
	send(t, c, host.Msg{Type: "proof", Proof: strings.Repeat("0", 64)})
	challenged(t, c, port, "a", tok1)
}

func TestServeRefusesAWrongProof(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", origin, tok1)
	bound(t, p, "b", origin, tok2)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	refused := func(name string, proof func(nonceS string) string) {
		t.Helper()
		c, err := dialAs(t, addr, "", origin)
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
	c, _ := dialAs(t, addr, "", origin)
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
	bound(t, p, "a", origin, tok1)
	bound(t, p, "b", origin, tok2)
	writeConfig(t, p, workUnion)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	tokens := map[string]string{"a": tok1, "b": tok2}
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
	nonceS := challenged(t, c, port, "a", tok1)
	send(t, c, host.Msg{Type: "proof", Proof: mac(tok1, "extension:"+nonceS)})
	if m := next(t, c); m.Code != "db-locked" {
		t.Fatalf("second connection for a: %+v", m)
	}
}

func TestServePings(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", origin, tok1)
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
	bound(t, p, "a", origin, tok1)
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
	bound(t, p, "a", origin, tok1)
	bound(t, p, "b", origin, tok2)
	writeConfig(t, p, workUnion)
	addr := serve(t, p, time.Hour)
	_, port, _ := net.SplitHostPort(addr)
	c, err := dialAs(t, addr, "", origin)
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
	bound(t, p, "a", origin, tok1)
	bound(t, p, "b", origin, tok2)
	writeConfig(t, p, workUnion)
	log := &logBuf{}
	addr := serveLog(t, p, time.Hour, log)
	_, port, _ := net.SplitHostPort(addr)
	c, err := dialAs(t, addr, "", origin)
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
	io.WriteString(nc, "GET / HTTP/1.1\r\nHost: "+addr+"\r\nOrigin: "+origin+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
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
	bound(t, p, "a", origin, tok1)
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
	bound(t, p, "a", origin, tok1)
	bound(t, p, "b", origin, tok2)
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
		c, err := dialAs(t, addr, "", origin)
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
	bound(t, p, "a", origin, tok1)
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

// A database file that exists but cannot be read is the server's problem:
// the log has the details, and the unproven peer only db-missing.
func TestServeTellsAnUnprovenPeerNoFileDetails(t *testing.T) {
	p := paths(t)
	bound(t, p, "a", origin, tok1)
	bound(t, p, "broken", origin, tok2)
	if err := os.WriteFile(p.ProfileFile("broken"), []byte("format: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &logBuf{}
	c, err := dialAs(t, serveLog(t, p, time.Hour, log), "", origin)
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
	bound(t, p, "a", origin, strings.ToUpper(tok1))
	log := &logBuf{}
	c, err := dialAs(t, serveLog(t, p, time.Hour, log), "", origin)
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
	bound(t, p, "a", origin, tok1)
	bound(t, p, "b", origin, tok2)
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
	c, err := dialAs(t, addr, "", origin)
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
```

`export_test.go` gains `SetHelloWait(t, d)`, which shortens `helloWait` until the test ends; its restore is registered before `serve`'s cleanup, so it runs after Serve has stopped and the race detector stays quiet. `server_internal_test.go` checks that a `wsConn.Read` cut short by the server's own close returns `io.EOF`.

- [ ] **Step 5: Run** `bin/dev run go test -count=1 ./internal/host/` → FAIL (`host.Listen` undefined).

- [ ] **Step 6: Write `internal/host/server.go`:**

```go
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
// for each. Hosts share nothing in memory: the global lock keeps their
// rounds apart, as it kept separate processes apart, since flock locks
// belong to the open file and not to the process. If ln fails, Serve stops
// its hosts and returns the error.
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
	sv := &server{ctx: ctx, o: o, addr: ln.Addr().String(), port: port}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: sv}
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
	ctx     context.Context // ends every host
	o       Options
	addr    string // the address Serve listens on, which the Host must name
	port    string
	conns   atomic.Int64 // numbers the connections in the log
	refused refusals
	hosts   hostGroup
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
		sv.refused.log(log, r)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	sock, err := ws.Upgrade(w, r, helloMax)
	if err != nil {
		log.Warn("bad handshake", "err", err)
		return
	}
	// Until it proves itself, a connection has helloWait to do so.
	sock.SetReadDeadline(time.Now().Add(helloWait))
	c := newWSConn(sock, sv.o.Ping)
	ho := sv.o
	ho.Log, ho.Origin, ho.Port = log, origin, sv.port
	err = Run(sv.ctx, c, ho)
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

// refusals logs refused handshakes: each at Debug, and at Info at most
// once a minute with how many there were since the last Info line. A page
// that keeps knocking costs a line a minute, and the first refusal still
// shows.
type refusals struct {
	mu   sync.Mutex
	n    int
	last time.Time
}

func (f *refusals) log(log *slog.Logger, r *http.Request) {
	h, o := slog.String("host", r.Host), slog.String("origin", r.Header.Get("Origin"))
	log.LogAttrs(r.Context(), slog.LevelDebug, "refused a connection", h, o)
	f.mu.Lock()
	f.n++
	if !f.last.IsZero() && time.Since(f.last) < time.Minute {
		f.mu.Unlock()
		return
	}
	n := f.n
	f.n, f.last = 0, time.Now()
	f.mu.Unlock()
	log.LogAttrs(r.Context(), slog.LevelInfo, "refused a connection", h, o, slog.Int("count", n))
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

// proven ends the hello wait and lifts the hello's size limit: the host
// calls it once the extension has proven which database it is.
// The host calls it between Reads: see Run.
func (c *wsConn) proven() {
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
```

Change the host package comment's first sentence to: "Package host runs sync rounds for one extension connection, and Serve accepts the connections." Keep native messaging in the comment ("or as the native messaging host Chrome starts") until Task 7.

- [ ] **Step 7: The command.** In `main.go`, add `serve` to `usageText` (`  serve       Run the server the extensions connect to`), the switch case `case "serve": return cmdServe(args[1:], stderr)`, and pull the log opening out of `cmdHost` so both use it:

```go
// openLog opens the log every host and the server append to, rotating it
// first if it is large. Two hosts starting at once may both rotate, and so
// lose the old log. The caller closes the file.
func openLog(p db.Paths) (*slog.Logger, *os.File, error) {
	if err := os.MkdirAll(p.State, 0o700); err != nil {
		return nil, nil, err
	}
	rotateErr := rotate(p.LogFile())
	f, err := os.OpenFile(p.LogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	log := slog.New(slog.NewTextHandler(f, nil)).With("pid", os.Getpid())
	if rotateErr != nil {
		log.Warn("cannot rotate the log", "err", rotateErr)
	}
	return log, f, nil
}

// cmdServe runs the server until it is told to stop. One runs per user:
// it holds the serve lock while it runs.
func cmdServe(args []string, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "mirrormark: usage: mirrormark serve")
		return 2
	}
	if refuseRoot("serve", "", stderr) {
		return 1
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	p, err := db.DefaultPaths()
	if err != nil {
		return fail(err)
	}
	unlock, err := db.TryLock(p.ServeLock())
	if errors.Is(err, db.ErrLocked) {
		return fail(errors.New("mirrormark is already running for this user"))
	}
	if err != nil {
		return fail(err)
	}
	defer unlock()
	log, f, err := openLog(p)
	if err != nil {
		return fail(err)
	}
	defer f.Close()
	ports, err := extension.Ports()
	if err != nil {
		return fail(err)
	}
	ln, err := host.Listen(ports)
	if err != nil {
		log.Warn("cannot listen", "err", err)
		return fail(err)
	}
	log.Info("listening", "addr", ln.Addr().String())
	fmt.Fprintf(stderr, "mirrormark: listening on %s; the log is %s\n", ln.Addr(), p.LogFile())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := host.Serve(ctx, ln, host.Options{Paths: p, Log: log}); err != nil {
		log.Warn("server stopped", "err", err)
		return fail(err)
	}
	return 0
}
```

`cmdHost` uses `openLog` and keeps the rest as it is.

- [ ] **Step 8: Test the single instance** in `main_test.go`:

```go
// A second server for the same user stops at once, before it listens.
func TestServeRunsOncePerUser(t *testing.T) {
	d := t.TempDir()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, d+"/"+v)
	}
	p, _ := db.DefaultPaths()
	unlock, err := db.TryLock(p.ServeLock())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	var out, errb bytes.Buffer
	if code := run([]string{"serve"}, strings.NewReader(""), &out, &errb); code != 1 || !strings.Contains(errb.String(), "already running") {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
}
```

- [ ] **Step 9: README.** Under "Files", add `~/.local/share/mirrormark/serve.lock` ("Held by the running `mirrormark serve`, so one runs per user"). Under the profile block's description, the two new fields `origin` and `token` ("written by `install` when it binds the profile; do not edit"). Add `serve` after `unpack` in "Upgrade, uninstall, unpack" (the README has no other command list), saying the extension still uses the native messaging host. Do not describe the service yet.

- [ ] **Review fixes.** Review of the first version found these; each has a regression test, and the code above includes them.
  - `Run` derives its own context and cancels it on return, so a host that stops on an error (`bad-token`, `db-missing`, `db-locked`, `unsupported-version`) ends its reader instead of leaving it, and the message it holds, until the server stops (`TestStoppedHostLeavesNoReader`). The test harness's `start` closes the host's input when `Run` returns, as a host process's stdin closes when it exits.
  - `handle` ignores every message but `hello` and `proof` until the host runs, with one Debug line, replacing the tree-only check (`TestServeIgnoresMessagesBeforeTheProof`, `TestMessagesBeforeTheHostRunsChangeNothing`).
  - `ws.(*Conn).SetReadLimit`. `Serve` upgrades with `helloMax` (4 KB) and `proven()` raises it to `maxIn` (`TestSetReadLimit`, `TestServeRefusesALargeMessageBeforeTheProof`). Until the host runs, `Run`'s reader takes one message and waits for the main loop to handle it (the `handled` channel), so the tree the extension sends straight after its proof is read after the limit is raised (`TestServeTakesALargeTreeRightAfterTheProof`).
  - `admit` refuses an Origin without the `chrome-extension://` or `moz-extension://` prefix before it reads any database. Refusals are logged at Debug each, and at Info at most once a minute with a count (`refusals`; `TestServeAdmitsOnlyExtensionOrigins`, `TestServeLogsRefusalsBriefly`).
  - A `db-missing` from the challenge names no file or error to the peer; the log has the details (`TestServeTellsAnUnprovenPeerNoFileDetails`).
  - A token that is not 64 lowercase hex digits gets `bad-token` and a log line saying it is malformed (`hex32`; `TestServeRefusesAMalformedToken`). The tests' tokens are 64 hex digits.
  - `Serve` derives its context and cancels it when `srv.Serve` returns, so a failed listener stops the hosts and `Serve` returns the error (`TestServeStopsItsHostsWhenTheListenerFails`). The handler is `server.ServeHTTP`, and the shutdown guard is `hostGroup`, so the 503 is tested directly (`TestServerRefusesHandshakesOnceShuttingDown`).

- [ ] **Step 10: Run** `bin/dev run make` → PASS; `bin/dev run make check-race` → PASS; `bin/dev run make check-e2e` → PASS (unchanged: no extension uses the server).

- [ ] **Step 11: Commit** `feat(rfc-0001): serve extension connections on 127.0.0.1 [new feature]`.

---

### Task 4: Hand each bound profile a token

**Files:**
- Create: `internal/db/bind.go`, `internal/db/bind_test.go`
- Modify: `internal/chrome/bind.go`, `internal/firefox/bind.go`, `cmd/mirrormark/install.go` (the two bind loops), `cmd/mirrormark/install_test.go` (`fakeChromeOnPath`, `geckoData`, and the bind-URL assertions at lines 383–384, 906, 1358, 1560), `extension/bind.js`, `e2e/install_test.go`, `README.md`

**Interfaces:**
- Consumes: Task 3 (`Profile.Origin`, `Profile.Token`).
- Produces: `db.Bind`; `Bind` in both browser packages taking `token`; `storage.local.token` in the extension.

The extension still uses native messaging and ignores the token; this task only puts it in place.

- [ ] **Step 1: Failing test** in `internal/db/bind_test.go`:

```go
package db_test

import (
	"regexp"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
)

func TestBindWritesTheOriginAndKeepsTheToken(t *testing.T) {
	d := t.TempDir()
	p := db.Paths{Config: d + "/c", Data: d + "/d", State: d + "/s"}
	if err := db.SaveDB(p.ProfileFile("a"), &db.DB{Format: 1, Profile: db.Profile{ID: "a", Label: "x/a", Browser: "firefox"}}); err != nil {
		t.Fatal(err)
	}
	tok, err := db.Bind(p, "a", "moz-extension://u1")
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(tok) {
		t.Fatalf("token %q, %v", tok, err)
	}
	again, err := db.Bind(p, "a", "moz-extension://u2")
	if err != nil || again != tok {
		t.Fatalf("second bind gave %q, %v; want the same token", again, err)
	}
	got, _ := db.LoadDB(p.ProfileFile("a"))
	if got.Profile.Origin != "moz-extension://u2" || got.Profile.Token != tok {
		t.Fatalf("profile %+v", got.Profile)
	}
}
```

- [ ] **Step 2: Run** `bin/dev run go test ./internal/db/` → FAIL (`db.Bind` undefined).

- [ ] **Step 3: `internal/db/bind.go`:**

```go
package db

import (
	"crypto/rand"
	"encoding/hex"
)

// Bind records origin as profile id's extension origin and returns its
// token, creating one if it has none. Running it again keeps the token, so
// a profile bound before stays bound. It holds the global lock, so a round
// that read the file before cannot write the old profile block back.
func Bind(p Paths, id, origin string) (string, error) {
	unlock, err := Lock(p.GlobalLock())
	if err != nil {
		return "", err
	}
	defer unlock()
	d, err := LoadDB(p.ProfileFile(id))
	if err != nil {
		return "", err
	}
	if d.Profile.Origin == origin && d.Profile.Token != "" {
		return d.Profile.Token, nil
	}
	if d.Profile.Token == "" {
		b := make([]byte, 32)
		rand.Read(b) // never fails
		d.Profile.Token = hex.EncodeToString(b)
	}
	d.Profile.Origin = origin
	return d.Profile.Token, SaveDB(p.ProfileFile(id), d)
}
```

- [ ] **Step 4: The bind pages carry it.** `chrome.Browser.Bind(bin, userDataDir, profileDir, extID, dbID, token string, reported …)` opens `chrome-extension://<extID>/bind.html#<dbID>.<token>`. `firefox.Browser.Bind(bin, profileDir, uuid, dbID, token string, reported …)` opens `moz-extension://<uuid>/bind.html#<dbID>.<token>`. Update both doc comments ("bind.html#<dbID>.<token>").

In `install.go`'s Chromium loop, just before `cb.Bind`:

```go
		token, err := db.Bind(p, id, "chrome-extension://"+ext.ID)
		if err != nil {
			return fail(err)
		}
```

and pass `token`. In the Gecko loop, `db.Bind(p, id, "moz-extension://"+uuid)` before `b.Bind`.

- [ ] **Step 5: The page stores it.** `extension/bind.js`:

```js
// Opened by `mirrormark install` in one profile as
// bind.html#<database id>.<token>. The page is not web-accessible, so web
// pages cannot open it.
const [db, token] = decodeURIComponent(location.hash.slice(1)).split(".");
const msg = document.getElementById("msg");
if (!/^[a-z0-9][a-z0-9-]*$/.test(db) || !/^[0-9a-f]{64}$/.test(token ?? "")) {
  msg.textContent = "This link does not name a mirrormark database.";
} else {
  // boundAt changes even when db does not, so a rebind always reconnects.
  chrome.storage.local.set({ db, token, boundAt: Date.now() }).then(() => {
    msg.textContent = `This profile now syncs as ${db}. You can close this tab.`;
    setTimeout(() => window.close(), 1500);
  });
}
```

- [ ] **Step 6: The fakes and assertions in `install_test.go`.** Both fake browser scripts take the database id before the dot:
  - `fakeChromeOnPath`: `"frag=${url##*#}; [ \"${frag%%.*}\" = \"$FAKE_NO_HELLO\" ] || touch -d \"@$(date +%s.%N)\" \"" + p.ProfilesDir() + "/${frag%%.*}.lock\"\n"`.
  - `geckoData`: `"case \"$url\" in *bind.html*) frag=${url##*#}; touch -d \"@$(date +%s.%N)\" \""+p.ProfilesDir()+"/${frag%%.*}.lock\";; esac\n"`.
  - Add a helper and use it in the four bind-URL assertions, so that `bind.html#chrome-personal` becomes `bind.html#chrome-personal.`+`tokenOf(t, p, "chrome-personal")`:

```go
// tokenOf is the token install wrote for database id.
func tokenOf(t *testing.T, p db.Paths, id string) string {
	t.Helper()
	d, err := db.LoadDB(p.ProfileFile(id))
	if err != nil || d.Profile.Token == "" {
		t.Fatalf("no token for %s: %+v, %v", id, d, err)
	}
	return d.Profile.Token
}
```

  - In the first of those tests (Chrome, the one asserting the calls at line 383), also check `origin`: `d.Profile.Origin == "chrome-extension://"+id`. In the Firefox one (line 906): `"moz-extension://5c1b2f2e-7d0a-4b8e-9b51-0a6c1d2e3f4b"`.

- [ ] **Step 7: The e2e check.** In `TestInstallAndBind`'s loop, after the `db` check:

```go
		if got := eval[string](t, c.b, `chrome.storage.local.get("token").then((r) => r.token)`); got != d.Profile.Token || got == "" {
			t.Errorf("%s holds token %q, the database %q", c.b.Name, got, d.Profile.Token)
		}
```

(move the `db.LoadDB` above it).

- [ ] **Step 8: README.** In "Installing and binding", say the bind page also stores the profile's token, which proves the binding to the server once the extension uses it, and that running `install` for a bound profile keeps its token.

- [ ] **Step 9: Run** `bin/dev run make` → PASS, `bin/dev run make check-e2e` → PASS.

- [ ] **Step 10: Commit** `feat(rfc-0001): hand each bound profile a token`.

---

### Task 5: `mirrormark serve` as a systemd user service

**Files:**
- Create: `cmd/mirrormark/service.go`, `cmd/mirrormark/service_test.go`
- Modify: `cmd/mirrormark/install.go` (install's two families, `cmdUninstall`, `uninstallFirefox`, `chromiumUsers`), `cmd/mirrormark/main_test.go` (`TestMain`), `README.md`

**Interfaces:**
- Consumes: Task 3 (`serve`).
- Produces: `runSystemctl`, `unitPath`, `unit`, `startServer`, `stopServer`, `serviceUsers`, `geckoUsers`; `chromiumUsers(except, id string, stderr io.Writer)`.

Install and uninstall of the service land together, so no prefix leaves a service that `uninstall` cannot remove. The service runs a server that no extension uses yet.

- [ ] **Step 1: No test touches the real systemd.** In `main_test.go`:

```go
func TestMain(m *testing.M) {
	// The tests' default: no systemd, as in a container. Tests of the
	// service install their own recorder.
	runSystemctl = func(args ...string) error { return errors.New("systemctl is not available in tests") }
	os.Exit(m.Run())
}
```

- [ ] **Step 2: Failing tests** in `service_test.go`:

```go
package main

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// fakeSystemctl records each systemctl call, failing those fail names.
func fakeSystemctl(t *testing.T, fail string) *[]string {
	calls := &[]string{}
	old := runSystemctl
	t.Cleanup(func() { runSystemctl = old })
	runSystemctl = func(args ...string) error {
		*calls = append(*calls, strings.Join(args, " "))
		if fail != "" && strings.HasPrefix(strings.Join(args, " "), fail) {
			return errors.New("Failed to connect to bus")
		}
		return nil
	}
	return calls
}

func TestUnit(t *testing.T) {
	got := string(unit("/opt/mirror mark/100%/mirrormark"))
	want := `# Written by mirrormark install; mirrormark uninstall removes it.
[Unit]
Description=mirrormark bookmark sync

[Service]
ExecStart="/opt/mirror mark/100%%/mirrormark" serve
Restart=on-failure

[Install]
WantedBy=default.target
`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// Every run restarts the server, so it runs the binary just installed.
func TestStartServer(t *testing.T) {
	p := testHome(t)
	calls := fakeSystemctl(t, "")
	var out bytes.Buffer
	for range 2 {
		*calls = nil
		if err := startServer(p, "/bin/mirrormark", &out); err != nil {
			t.Fatal(err)
		}
		if want := []string{"daemon-reload", "enable mirrormark.service", "restart mirrormark.service"}; !slices.Equal(*calls, want) {
			t.Fatalf("calls %q", *calls)
		}
	}
	data, err := os.ReadFile(unitPath(p))
	if err != nil || !bytes.Contains(data, []byte("ExecStart=/bin/mirrormark serve\n")) {
		t.Fatalf("unit %q, %v", data, err)
	}
}

func TestStartServerWithoutSystemd(t *testing.T) {
	p := testHome(t)
	fakeSystemctl(t, "daemon-reload")
	var out bytes.Buffer
	if err := startServer(p, "/bin/mirrormark", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Keep this running instead") || !strings.Contains(out.String(), "/bin/mirrormark serve") {
		t.Fatalf("output %q", out.String())
	}
}

func TestStopServer(t *testing.T) {
	p := testHome(t)
	calls := fakeSystemctl(t, "")
	var out bytes.Buffer
	startServer(p, "/bin/mirrormark", &out)
	*calls = nil
	if err := stopServer(p, []string{"Firefox Developer Edition"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 || !strings.Contains(out.String(), "Kept the mirrormark service, which Firefox Developer Edition still uses.") {
		t.Fatalf("calls %q, output %q", *calls, out.String())
	}
	if err := stopServer(p, nil, &out); err != nil {
		t.Fatal(err)
	}
	if want := []string{"disable --now mirrormark.service", "daemon-reload"}; !slices.Equal(*calls, want) {
		t.Fatalf("calls %q", *calls)
	}
	if _, err := os.Stat(unitPath(p)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unit still there: %v", err)
	}
}
```

`testHome` (line 149 of `install_test.go`) sets the XDG variables to a temporary directory and returns its `db.Paths`. Then extend the existing cross-browser tests:
  - In the Chromium test that installs Chrome and Brave and uninstalls one (`installChromium`, line 1316, and its callers): after `uninstall --browser brave` with Chrome's file in place, the unit is still there; after both, it is gone.
  - In `installGecko` (line 1593) and its callers: the same for LibreWolf and Developer Edition.
  - New: Chrome installed, a Developer Edition profile installed, `uninstall --browser chrome` keeps the unit and prints "Kept the mirrormark service, which Firefox Developer Edition still uses."; then `uninstall --browser firefox` removes it.

- [ ] **Step 3: Run** `bin/dev run go test ./cmd/mirrormark/` → FAIL (`unit` undefined).

- [ ] **Step 4: Write `service.go`:**

```go
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chriscz/mirrormark/internal/chrome"
	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/firefox"
)

// runSystemctl runs systemctl --user; tests replace it.
var runSystemctl = func(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}

const unitName = "mirrormark.service"

// unitPath is the user unit. systemd reads user units from
// $XDG_CONFIG_HOME/systemd/user, the directory p.Config sits in.
func unitPath(p db.Paths) string {
	return filepath.Join(filepath.Dir(p.Config), "systemd", "user", unitName)
}

// unitQuote writes s for a unit file: % and $ doubled so systemd does not
// expand them, and quoted if it holds a space, a quote or a backslash.
func unitQuote(s string) string {
	s = strings.NewReplacer("%", "%%", "$", "$$").Replace(s)
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// unit is the user unit that runs self as the server. It sets no
// environment: systemd's user manager supplies it.
func unit(self string) []byte {
	return []byte("# Written by mirrormark install; mirrormark uninstall removes it.\n[Unit]\nDescription=mirrormark bookmark sync\n\n[Service]\n" +
		"ExecStart=" + unitQuote(self) + " serve\nRestart=on-failure\n\n[Install]\nWantedBy=default.target\n")
}

// startServer writes the user unit and has systemd (re)start the server,
// every time, so it runs the binary just installed; connected extensions
// reconnect at their next alarm. Without systemd it says how to run the
// server by hand.
func startServer(p db.Paths, self string, stdout io.Writer) error {
	if err := chrome.WriteFiles([]chrome.File{{Path: unitPath(p), Data: unit(self)}}); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", unitName}, {"restart", unitName}} {
		if err := runSystemctl(args...); err != nil {
			fmt.Fprintf(stdout, "Could not run mirrormark as a systemd user service (%v).\nKeep this running instead, for example from your desktop's autostart:\n  %s serve\n", err, quoteArg(self))
			return nil
		}
	}
	return nil
}

// stopServer stops and removes the user unit unless users, the other
// browsers that still have mirrormark, need the server.
func stopServer(p db.Paths, users []string, stdout io.Writer) error {
	path := unitPath(p)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if len(users) > 0 {
		fmt.Fprintf(stdout, "Kept the mirrormark service, which %s still uses.\n", strings.Join(users, " and "))
		return nil
	}
	stopped := runSystemctl("disable", "--now", unitName) == nil
	if err := chrome.RemoveFiles([]string{path}); err != nil {
		return err
	}
	runSystemctl("daemon-reload")
	if stopped {
		fmt.Fprintln(stdout, "Stopped and removed the mirrormark service.")
	} else {
		fmt.Fprintf(stdout, "Removed %s. If you run mirrormark serve yourself, stop it.\n", path)
	}
	return nil
}

// serviceUsers names the browsers other than browser that still have
// mirrormark in place, so still need the server: another Chromium browser
// whose external file is there, or a Gecko browser with the XPI in a
// profile a database names. One that cannot be checked counts.
func serviceUsers(p db.Paths, browser string, stderr io.Writer) []string {
	var out []string
	if key, err := chrome.LoadKey(filepath.Join(p.Data, "extension.pem")); err == nil {
		out = chromiumUsers(browser, chrome.ExtensionID(chrome.PublicKeyDER(key)), stderr)
	}
	dbs, errs := db.LoadAll(p)
	if len(errs) > 0 {
		return append(out, "a browser whose database cannot be read")
	}
	return append(out, geckoUsers(dbs, browser, stderr)...)
}
```

- [ ] **Step 5: Share the "who still uses it" checks.** In `install.go`:
  - `chromiumUsers(b chrome.Browser, id, stderr)` becomes `chromiumUsers(except, id string, stderr io.Writer)`, comparing `o.Config == except`; its caller passes `cb.Config`.
  - Move the loop in `uninstallFirefox` that fills `users` into:

```go
// geckoUsers names the Gecko browsers other than except whose profiles,
// as named by dbs, still hold the add-on. A profile it cannot check counts.
func geckoUsers(dbs map[string]*db.DB, except string, stderr io.Writer) []string {
	var users []string
	for _, d := range dbs {
		o, ok := firefox.Lookup(d.Profile.Browser)
		if !ok || o.Config == except || slices.Contains(users, o.Name) {
			continue
		}
		_, err := os.Stat(firefox.XPIPath(d.Profile.Path))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(stderr, "mirrormark: warning: cannot tell whether %s still uses mirrormark, so keeping the files it shares: %v\n", o.Name, err)
		}
		users = append(users, o.Name)
	}
	return users
}
```

  and in `uninstallFirefox`, `users := geckoUsers(dbs, b.Config, stderr)`, with the loop keeping only its `d.Profile.Browser == b.Config` half.

- [ ] **Step 6: Call them.**
  - Chromium install: after `ensureConfig`, `if err := startServer(p, self, stdout); err != nil { return fail(err) }`.
  - Gecko install: the same, after `ensureConfig` and writing `firefox.Files`.
  - `cmdUninstall`, Chromium branch, after `removeSystemFiles`: `if err := stopServer(p, serviceUsers(p, cb.Config, stderr), stdout); err != nil { return fail(err) }`. Gecko branch, after `uninstallFirefox`: the same with `fb.Config`.

- [ ] **Step 7: README.** In each Install section, one line after the first `install`: "`install` also sets up `mirrormark serve` as a systemd user service (`~/.config/systemd/user/mirrormark.service`) and starts it. Without systemd, it prints the command to keep running." In "Upgrade, uninstall, unpack": `install` restarts the service on every run, so run it after upgrading the binary, as before; uninstall removes the service once no other browser uses mirrormark. Under Files, add the unit, and say that if you set `XDG_CONFIG_HOME`, `XDG_DATA_HOME` or `XDG_STATE_HOME` in your shell, set them for systemd too in `~/.config/environment.d/`.

- [ ] **Step 8: Run** `bin/dev run make` → PASS, `bin/dev run make check-e2e` → PASS. In the dev image, `install` prints the "Keep this running instead" lines; the e2e tests do not depend on them.

- [ ] **Step 9: Commit** `feat(rfc-0001): run mirrormark serve as a systemd user service [new feature]`.

---

### Task 6: The extension connects to `mirrormark serve`

This is the switch. It is one commit because each part breaks without the others. The extension's new client needs the server's challenge. The e2e tests need the harness to start the server. `install`'s bind confirmation needs a running server.

**Files:**
- Modify: `extension/manifest.json`, `extension/background.js`, `extension/embed.go` (doc comment), `extension/embed_test.go`, `cmd/mirrormark/service.go` (`serverRunning`), `cmd/mirrormark/install.go`, `cmd/mirrormark/install_test.go`, `e2e/env.go`, `e2e/install_test.go`, `README.md`

**Interfaces:**
- Consumes: Tasks 3, 4 and 5.
- Produces: `serverRunning`, `e.serve(t)`, `e.stopServer(t)`, `connections(t, e, id)`, `closed(t, e, conn)`.

- [ ] **Step 1: Manifest.** In `manifest.json`: `"version": "0.2.0"`, `"permissions": ["alarms", "bookmarks", "storage"]`, and add `"content_security_policy": { "extension_pages": "script-src 'self'" }`. In `embed_test.go`: expect `alarms,bookmarks,storage`; assert the CSP is exactly that string; delete `TestBackgroundUsesHostName`.

- [ ] **Step 2: `background.js`.** Replace the header comment, the `HOST` constant and the connection code. The bookmark, apply, `realIndex`, `matches`, `applyOp` and `setStatus` code stays as it is, with `port` renamed `sock`.

```js
// mirrormark: sends this profile's bookmark tree to the local mirrormark
// server and applies the changes it sends back. It stays idle, with no
// connection, until `mirrormark install` binds the profile to a database.
// Chrome and Firefox install it in every bound profile, so it never
// uninstalls itself.
const V = 1;
const QUIET_MS = 500; // send a tree after this long without bookmark events
const REBIND_MS = 1000; // time for the old connection's host to let go of the database
// Reconnects wait for an alarm, which the browser fires no sooner than 30 s.
const MIN_BACKOFF = 30000;
const MAX_BACKOFF = 60000;
const DIAL_MS = 5000; // a port that does not finish the challenge in this time is skipped
const RECONNECT = "reconnect"; // the one-shot alarm after a connection fails or closes
// A browser that stops an idle background closes its socket without
// running onclose; this alarm starts the background again, and it
// reconnects.
const TICK = "tick";
const NOT_RUNNING =
  "mirrormark is not running. Start it with `systemctl --user start mirrormark`, or run `mirrormark serve`.";
// The ports the server may listen on, in the order it tries them. The
// binary reads the same file.
const PORTS = fetch(chrome.runtime.getURL("ports.json")).then((r) => r.json());
```

Keep `ROOTS`, `FIREFOX_ROOTS`, `BROWSER`/`VERSION` and `isSeparator`. State: `let sock = null;` in place of `port`, the rest as today. Listeners:

```js
chrome.alarms.onAlarm.addListener(async (a) => {
  if (a.name === TICK) {
    connect(); // which does nothing while connected or while a reconnect waits
    return;
  }
  if (a.name !== RECONNECT) return;
  await chrome.alarms.clear(RECONNECT);
  connect();
});
chrome.alarms.get(TICK).then((a) => a ?? chrome.alarms.create(TICK, { periodInMinutes: 1 }));
```

In the `storage.onChanged` listener, `if (!(changes.db || changes.token || changes.boundAt)) return;` (keeping the area check), and `if (sock) sock.close(); sock = null;` replaces the port lines. In `changed()`, `if (!sock)`, `post(sock, …)`. The connection:

```js
async function open() {
  if (sock || rebinding) return;
  // After a failed connection the alarm sets the pace, not bookmark events
  // or a restarted worker.
  if (await chrome.alarms.get(RECONNECT)) return;
  const bound = binding;
  const { db, token } = await chrome.storage.local.get(["db", "token"]);
  if (!db) return; // not bound: stay dormant
  const { sock: s, failure, hostError: refused } = await dial(db, token ?? "");
  if (bound !== binding || sock || rebinding) {
    s?.close(); // rebound meanwhile
    return;
  }
  if (!s) {
    await setStatus({ error: failure ?? NOT_RUNNING, problems: [] });
    // A server that refused this profile will refuse it again; don't ask
    // every 30 s.
    chrome.alarms.create(RECONNECT, { when: Date.now() + (refused ? MAX_BACKOFF : MIN_BACKOFF) });
    return;
  }
  refs.clear();
  const opened = Date.now();
  let hostError = false;
  sock = s;
  s.onmessage = (ev) => {
    let m;
    try {
      m = JSON.parse(ev.data);
    } catch (e) {
      console.warn("mirrormark: bad message", e);
      return;
    }
    if (m.type === "ping") {
      // Gecko stops an event page 30 s after its last extension event,
      // socket traffic or not; an API call counts as one.
      chrome.runtime.getPlatformInfo();
      return;
    }
    if (m.type === "error") {
      // The host stops after an error; don't reconnect every 30 s. Set
      // here, not in the queue, so the close that follows already waits
      // the longer time.
      hostError = true;
      backoff = MAX_BACKOFF;
    }
    queue = queue
      .then(() => onMessage(s, m))
      .catch((e) => console.warn("mirrormark: handling", m.type, "failed:", e));
  };
  s.onclose = (ev) => {
    if (sock !== s) return; // replaced by a newer connection
    sock = null;
    // The host's own error says more than the close.
    if (!hostError) setStatus({ error: `the connection to mirrormark closed (code ${ev.code})` });
    if (Date.now() - opened > MAX_BACKOFF) backoff = MIN_BACKOFF;
    chrome.alarms.create(RECONNECT, { when: Date.now() + backoff });
    backoff = Math.min(backoff * 2, MAX_BACKOFF);
  };
  // The hello and the proof went out in dial, so no tree can precede them.
  await sendTree();
}

// dial returns { sock } for the first port whose server proves it holds
// this profile's token, once the extension has sent its own proof. If none
// does, it returns the first answer to show instead: { failure, hostError }
// for a server's error, { failure } for a wrong proof, or {} if nothing
// answered. A port where nothing listens fails at once, and so does one
// whose server refuses the upgrade, such as another user's mirrormark,
// which does not know this profile's origin.
async function dial(db, token) {
  let first = {};
  for (const p of await PORTS) {
    const r = await challenge(p, db, token);
    if (r.sock) return r;
    if (first.failure === undefined) first = r;
  }
  return first;
}

// challenge runs the hello on port p. The server must prove it holds the
// token before the extension proves it does, so a program posing as
// mirrormark learns only the database id and a nonce it cannot use. The
// token itself never goes on the socket.
function challenge(p, db, token) {
  return new Promise((resolve) => {
    const s = new WebSocket(`ws://127.0.0.1:${p}/`);
    const nonceE = hex(crypto.getRandomValues(new Uint8Array(32)));
    let over = false;
    const done = (r) => {
      if (over) return;
      over = true;
      clearTimeout(t);
      s.onopen = s.onmessage = s.onerror = s.onclose = null;
      if (!r.sock) s.close();
      resolve(r);
    };
    const t = setTimeout(() => done({}), DIAL_MS);
    s.onerror = s.onclose = () => done({});
    s.onopen = () => post(s, { v: V, type: "hello", db, nonce: nonceE, browser: BROWSER.toLowerCase() + "/" + VERSION });
    s.onmessage = async (ev) => {
      let m;
      try {
        m = JSON.parse(ev.data);
      } catch {
        m = {};
      }
      if (m.type === "ping") return;
      if (m.type === "error") return done({ failure: `${m.code}: ${m.message}`, hostError: true });
      // A plain comparison is enough here: each attempt has a new nonce,
      // so timing it teaches nothing about the next.
      if (m.type !== "challenge" || m.proof !== (await mac(token, `server:${p}:${nonceE}`))) {
        return done({ failure: `the program on port ${p} could not prove it is your mirrormark` });
      }
      const proof = await mac(token, `extension:${m.nonce}`);
      if (over) return; // timed out meanwhile
      post(s, { v: V, type: "proof", proof });
      done({ sock: s });
    };
  });
}

// mac is HMAC-SHA256 of msg, keyed by the token as text, in lowercase hex,
// as the server computes it.
async function mac(token, msg) {
  const enc = new TextEncoder();
  const key = await crypto.subtle.importKey("raw", enc.encode(token), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return hex(new Uint8Array(await crypto.subtle.sign("HMAC", key, enc.encode(msg))));
}

function hex(bytes) {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

function post(s, m) {
  if (s.readyState !== WebSocket.OPEN) {
    console.warn("mirrormark: cannot send", m.type, "on a closed connection");
    return;
  }
  s.send(JSON.stringify(m));
}
```

`sendTree` reads `const p = sock;`, and `onMessage(p, m)` compares `p !== sock`. An unbound profile with no token sends nothing: `open` returns at `if (!db)`. A profile bound by a build before the token has a `db` but no `token`; its hello gets `bad-token` from the server, since the database has no token either, and the popup says to run `install` again.

- [ ] **Step 3: `install` binds only with a server running.** In `service.go`:

```go
// serverWait is how long install waits for a server systemd is starting.
var serverWait = 5 * time.Second

// serverRunning reports whether a server holds the serve lock. A server
// that starts during the brief test finds the lock taken, exits, and
// systemd starts it again.
func serverRunning(p db.Paths) bool {
	deadline := time.Now().Add(serverWait)
	for {
		unlock, err := db.TryLock(p.ServeLock())
		if errors.Is(err, db.ErrLocked) {
			return true
		}
		if err == nil {
			unlock()
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
```

In `install.go`, in both families, after the `!ready` / files-changed return and before the bind loop:

```go
	if !serverRunning(p) {
		fmt.Fprintf(stdout, "mirrormark is not running, so no profile can report in. Start it with systemctl --user start mirrormark, or run mirrormark serve, then run %s again.\n", again(cb.InstallCommand(), uddFlag, chosen[0].ID()))
		return 1
	}
```

(Gecko: `again("mirrormark install --browser "+b.Config, udd, targets[0].c.ID())`.)

In `install_test.go`, the bind fakes stand for the extension's hello, so they stand for the server too. Add:

```go
// fakeServer holds the serve lock as a running server would.
func fakeServer(t *testing.T, p db.Paths) {
	unlock, err := db.TryLock(p.ServeLock())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
}
```

Call it in `fakeChromeOnPath` and `geckoData`. Add one test per family in which no server runs: the second pass prints "mirrormark is not running", exits 1, and the fake browser's call log stays empty. Set `serverWait = 0` in those tests.

- [ ] **Step 4: The e2e harness starts the server.** In `e2e/env.go`, add `server *exec.Cmd` to `Env`, call `e.serve(t)` at the end of `newEnv`, and:

```go
// serve starts the test's mirrormark server, as the user's systemd service
// would; the dev image has no user session for systemd. It stops when the
// test ends, before the home directory goes.
func (e *Env) serve(t *testing.T) {
	t.Helper()
	cmd := exec.Command(bin, "serve")
	cmd.Env = e.env
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	e.server = cmd
	t.Cleanup(func() { e.stopServer(t) })
	eventually(t, 5*time.Second, "the server to listen", func() bool {
		b, _ := os.ReadFile(e.Paths.LogFile())
		return bytes.Count(b, []byte("msg=listening")) > e.listens
	})
	e.listens++
}

// stopServer stops the test's server, as a crash or systemctl stop would.
func (e *Env) stopServer(t *testing.T) {
	if e.server == nil {
		return
	}
	e.server.Process.Signal(syscall.SIGTERM)
	e.server.Wait()
	e.server = nil
}
```

Add `listens int // how many times a server of this test has listened` to `Env`. Imports: `bytes`, `syscall`.

In `e2e/install_test.go`, replace `connectedRe`/`hostPIDs` with connections, since one server now holds every host:

```go
var (
	connectedRe    = regexp.MustCompile(`msg=connected pid=\d+ conn=(\d+) db=(\S+)`)
	disconnectedRe = regexp.MustCompile(`msg=disconnected pid=\d+ conn=(\d+)`)
)

// connections lists the numbers of the connections whose hello for
// database id the server accepted, in the order of the log.
func connections(t *testing.T, e *Env, id string) []string {
	t.Helper()
	var out []string
	for _, m := range connectedRe.FindAllStringSubmatch(hostLog(t, e), -1) {
		if m[2] == id {
			out = append(out, m[1])
		}
	}
	return out
}

// closed reports whether connection conn has ended.
func closed(t *testing.T, e *Env, conn string) bool {
	for _, m := range disconnectedRe.FindAllStringSubmatch(hostLog(t, e), -1) {
		if m[1] == conn {
			return true
		}
	}
	return false
}
```

In `TestInstallAndBind` and `TestFirefoxInstallAndBind`, `hostPIDs` becomes `connections`, "a new host for a" becomes "a new connection for a", and `exited(old[…])` with "the old host to exit" becomes `closed(t, e, old[…])` with "the old connection to close". Keep `exited`, which the browser helpers use. The count of `"msg=connected pid="` stays.

New test, in `e2e/install_test.go` (imports: `encoding/json`, `net`, `net/http`, `slices`, `strconv`, `sync`, `extension`, `internal/host`, `internal/ws`):

```go
// impostor holds the first port with a program that accepts any upgrade
// and answers every hello with a challenge it cannot back, as another
// user's program could. It returns the types of the messages it received.
func impostor(t *testing.T) func() []string {
	ports, err := extension.Ports()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[0])))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Upgrade(w, r, 1<<20)
		if err != nil {
			return
		}
		defer c.Close(1000, "")
		for {
			b, err := c.Read()
			if err != nil {
				return
			}
			var m host.Msg
			json.Unmarshal(b, &m)
			mu.Lock()
			got = append(got, m.Type)
			mu.Unlock()
			if m.Type == "hello" {
				b, _ := json.Marshal(host.Msg{V: host.Version, Type: "challenge", Nonce: strings.Repeat("0", 64), Proof: strings.Repeat("0", 64)})
				c.Write(b)
			}
		}
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

// TestReconnectsAfterServerRestart: with the server gone, the popup says
// mirrormark is not running. Once it is back, on the second port because a
// program that cannot prove itself holds the first, the extension skips
// that program, tells it nothing more than its hello, and syncs again.
func TestReconnectsAfterServerRestart(t *testing.T) {
	e := setup(t)
	roundDone(t, e.A, "a's first round")
	e.stopServer(t)
	// The close sets the 30 s reconnect alarm; the attempt it starts finds
	// no server.
	eventually(t, 70*time.Second, "the popup to say mirrormark is not running", func() bool {
		return eval[bool](t, e.A, `chrome.storage.local.get("status").then((r) => (r.status?.error ?? "").startsWith("mirrormark is not running"))`)
	})
	got := impostor(t)
	before := time.Now()
	e.serve(t) // the first port is taken, so it listens on the second
	eventually(t, 70*time.Second, "a proven connection after the server came back", func() bool {
		return db.Connected(e.Paths, e.DBA, before)
	})
	roundDone(t, e.A, "a round after the restart")
	if types := got(); !slices.Contains(types, "hello") || slices.Contains(types, "proof") || slices.Contains(types, "tree") {
		t.Fatalf("the program on the first port received %q; want hellos only", types)
	}
}
```

- [ ] **Step 5: Run** `bin/dev run make` → PASS. Then `bin/dev run make check-e2e` → PASS, the whole suite.
  - If a Firefox or LibreWolf socket closes with code 1015, the CSP did not take: check the unpacked manifest in the XPI.
  - If Gecko's first socket drops about 10 s after start (seen in the spike), the 30 s alarm covers it. A test that waits less than that after a Firefox start must not be tightened to hide it; widen its wait and say why in a comment.
  - If a test sees `bad-token`, either the binding predates Task 4's token (every e2e binding runs `install` after this change, so stop and report) or the two sides compute the proof differently: compare the extension's `mac` with the Go one on one input in the browser's console before changing anything.
  - If `crypto.subtle` is undefined in a background, the context is not secure: report it with the browser and version. Do not fall back to sending the token.

- [ ] **Step 6: README.** Replace "The popup says Chrome cannot reach the host" with "The popup says mirrormark is not running": start it with `systemctl --user start mirrormark` or `mirrormark serve`; check `systemctl --user status mirrormark` and the log. Add `bad-token: …` under "What the popup shows": run `install --profile` for that profile again; and "the program on port N could not prove it is your mirrormark": another program holds that port; if sync does not resume, find it with `ss -ltnp`. In "Upgrade", say that the first `install` after upgrading from a build before the server asks for a browser restart, and that the second binds each profile again.

- [ ] **Step 7: Commit** `feat(rfc-0001): connect the extension to mirrormark serve`.

---

### Task 7: Remove native messaging

**Files:**
- Modify: `cmd/mirrormark/main.go`, `cmd/mirrormark/main_test.go`, `cmd/mirrormark/install.go`, `cmd/mirrormark/install_test.go`, `internal/host/protocol.go`, `internal/host/host.go`, `internal/host/host_test.go`, `internal/host/protocol_test.go`, `internal/host/export_test.go`, `internal/chrome/files.go`, `internal/chrome/profiles.go` (comment at line 86), `internal/chrome/chrome_test.go`, `internal/firefox/files.go`, `internal/firefox/profiles.go` (package comment), `internal/firefox/firefox_test.go`, `extension/embed.go`, `README.md`

**Interfaces:**
- Produces: `chrome.LegacyHostManifest`, `firefox.LegacyHostManifest()`; `Browser.SystemFiles` returns one file; `firefox.Files` returns no host manifest.

`install` leaves a host manifest from an earlier build in place: nothing starts it any more. `uninstall` removes it as it does today, by its new name.

- [ ] **Step 1: Failing tests for old host manifests.** In `install_test.go`, beside `TestUninstallLeavesAnotherExtensionsHostManifest`:

```go
// uninstall removes the native host manifest a build before the server
// left, when it is this install's.
func TestUninstallRemovesTheOldHostManifest(t *testing.T) {
	skipIfRoot(t)
	p := testHome(t)
	root, _ := fakeSystem(t)
	var out, errb bytes.Buffer
	run([]string{"unpack"}, strings.NewReader(""), &out, &errb) // creates the key
	key, err := chrome.LoadKey(filepath.Join(p.Data, "extension.pem"))
	must(t, err)
	legacy := filepath.Join(root, chrome.LegacyHostManifest)
	// Root's directories are read-only here; put it in place as sudo would.
	must(t, runSudo(strings.NewReader(`{"allowed_origins": ["chrome-extension://`+chrome.ExtensionID(chrome.PublicKeyDER(key))+`/"]}`), io.Discard, io.Discard,
		"install", "-D", "-m", "0644", "/dev/stdin", legacy))
	if code := run([]string{"uninstall"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if _, err := os.Stat(legacy); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("old host manifest after uninstall: %v", err)
	}
}
```

And `TestFirefoxUninstallRemovesTheOldHostManifest`: `geckoData`, a first-pass `install --browser firefox`, then `firefox.LegacyHostManifest()` written with `{"allowed_extensions": ["`+extension.GeckoID+`"]}`; after `uninstall --browser firefox` it is gone, and one naming `someone-else@example.org` stays.

- [ ] **Step 2: Run** `bin/dev run go test ./cmd/mirrormark/` → FAIL (`chrome.LegacyHostManifest` undefined).

- [ ] **Step 3: The browser packages.**
  - `internal/chrome/files.go`: delete `SystemHostDir`, `hostManifestJSON`, `HostManifestPath`; `indented` takes only `externalJSON`; `SystemFiles(e Extension) []File` returns just the external file. Its comment says "the file that tells b to install the extension". Add:

```go
// LegacyHostManifest is where builds before the server put the native
// host manifest every Chromium browser read. uninstall removes it when it
// names this install's extension.
const LegacyHostManifest = "/etc/opt/chrome/native-messaging-hosts/com.github.chriscz.mirrormark.json"
```

  - `internal/firefox/files.go`: delete `hostManifestJSON` and the host manifest at the head of `Files(profileDirs []string)`, which loses its `bin` parameter; rename `HostManifestPath` to `LegacyHostManifest`, with the comment "where builds before the server put the native host manifest Firefox and LibreWolf read; uninstall removes it when it names the gecko id". It builds the path from the literal `com.github.chriscz.mirrormark.json`.
  - `extension/embed.go`: delete `HostName`; the package comment says the extension connects to the local server.
  - Tests: `chrome_test.go` (lines 326–335) expects one system file; `firefox_test.go` (161–173) checks `LegacyHostManifest`'s path and that `Files` holds only XPIs and `user.js` files.

- [ ] **Step 4: `install.go`.**
  - Chromium install: `system := cb.SystemFiles(ext)`. Delete the "lets another extension … replacing it" warning.
  - Gecko install: `firefox.Files(dirs)`.
  - Chromium uninstall: `host` becomes `legacy := filepath.Join(sysroot, chrome.LegacyHostManifest)`, appended to `paths` whenever it names the id, whatever other browsers do. The message becomes "Kept the CRX, which %s still uses." The no-key message names only the external file.
  - Gecko uninstall: the host manifest goes if it names the gecko id; `users` and its message go, since the only shared thing left is the service, which `stopServer` handles.
  - `selfPath()` is still used by `startServer`.

- [ ] **Step 5: The host and `main.go`.**
  - `main.go`: delete the host-mode check (`chrome-extension://`, `…json`, `host`), `cmdHost`, and `host` from `usageText`; drop the `strings` import if unused.
  - `main_test.go`: delete `TestExtensionOriginRunsTheHost` and `TestFirefoxArgumentsRunTheHost`. Rewrite `TestHostRotatesALargeLog` to open the log through `openLog` instead of running the host.
  - `protocol.go`: delete `Stream`, `stream`, `ReadMsg`, `WriteMsg` and the `encoding/binary` import. `protocol_test.go` tests `encode`/`decode` through `export_test.go` (`var Encode, Decode = encode, decode`): a round trip, oversize refused by `Encode`, bad JSON refused by `Decode`. `TestReadMsgRefusesOversize` and `TestReadMsgRefusesTruncated` go, since the WebSocket codec has its own limits and tests. `TestWriteMsgRefusesOversize` becomes an `Encode` test, and `TestSplitRefusesAnOpTooLargeToSend` (line 129) calls `host.Encode` in place of `host.WriteMsg`.
  - `host.go`: `hello` always calls `challenge`; the `if s.o.Origin != ""` branch and its comment go, since every connection comes through `Serve`. The package comment loses native messaging.
  - `host_test.go`: the fake extension talks to `Run` in memory:

```go
// end is one end of an in-memory connection between the fake extension
// and the host. Closing one end tells the other it has gone.
type end struct {
	in   <-chan host.Msg
	out  chan<- host.Msg
	gone <-chan struct{}
	self chan struct{}
	once sync.Once
}

func pipe() (h, x *end) {
	hx, xh := make(chan host.Msg), make(chan host.Msg)
	hs, xs := make(chan struct{}), make(chan struct{})
	return &end{in: xh, out: hx, gone: xs, self: hs}, &end{in: hx, out: xh, gone: hs, self: xs}
}

func (e *end) Read() (host.Msg, error) {
	select {
	case m := <-e.in:
		return m, nil
	case <-e.gone:
		return host.Msg{}, io.EOF
	}
}

func (e *end) Write(m host.Msg) error {
	select {
	case e.out <- m:
		return nil
	case <-e.gone:
		return io.ErrClosedPipe
	}
}

func (e *end) Close() { e.once.Do(func() { close(e.self) }) }
```

  In `start`: `h, x := pipe()`; `ho.Origin = testOrigin`; `host.Run(ctx, h, ho)` then `h.Close()`; the reader loop calls `x.Read()`; `send` calls `x.Write(m)`; the cleanup calls `x.Close()` where it closed `ew`. `ho.Port = "0"`. `addProfile` writes `Origin: testOrigin, Token: "t-" + id`, with `const testOrigin = "chrome-extension://abcdefghijklmnopabcdefghijklmnop"` (and `server_test.go`'s `origin` reuses it). The fake extension's hello sends `Nonce: nonceE` (`server_test.go`'s, shared), and its reader loop answers a `challenge` by checking `m.Proof == mac("t-"+dbID, "server:0:"+nonceE)` (failing the test if not) and sending `{Type: "proof", Proof: mac("t-"+dbID, "extension:"+m.Nonce)}` before anything else, using `server_test.go`'s `mac`. `TestOtherVersionIsRefused` uses `pipe()` too. The tests whose subject is a missing or wrong database keep their codes; the challenge's own cases stay in `server_test.go`.

- [ ] **Step 6: Install tests.** In `install_test.go`, delete `TestInstallWarnsWhenItReplacesAnotherExtensionsHostManifest`. Keep `TestUninstallLeavesAnotherExtensionsHostManifest`, pointing it at `chrome.LegacyHostManifest`. Make `TestUninstallingOneGeckoBrowserKeepsTheSharedHostManifest` and `TestGeckoUninstallKeepsTheHostManifestWhenItCannotCheck` about the service: `unitPath(p)` stays, and the "keeping the files it shares" warning. Remove `chrome.HostManifestPath()` and `firefox.HostManifestPath()` from every `mustExist`/`mustBeGone` and path list (lines 332, 734, 748, 887, 959, 992, 1092, 1340, 1377, 1391, 1472, 1490, 1518, 1547, 1587). Where such a list checked that a shared file stays, `filepath.Join(p.Data, "mirrormark.crx")` still does that for Chromium, and `unitPath(p)` for Gecko.

- [ ] **Step 7: README.** Remove every host-manifest passage: Chrome's "What `sudo` does" (one file now), "Ids, labels and the key" (two installs no longer contend for one manifest), the Brave and LibreWolf shared-manifest lines, and the Files table rows. Add one line to Upgrade: a native host manifest an earlier build left does nothing now, and `uninstall` removes it. Remove the troubleshooting text about native messaging errors.

- [ ] **Step 8: Run** `bin/dev run make` → PASS (`gofmt`, `vet` with the e2e tag, tests). `grep -rn "native\|HostName\|HostManifestPath\|connectNative" --include=*.go --include=*.js cmd internal extension e2e` → only the two legacy-manifest comments and constants. Then `bin/dev run make check-e2e` → PASS.

- [ ] **Step 9: Commit** `refactor(rfc-0001): remove native messaging`.

---

### Task 8: Install and bind Brave and LibreWolf end to end

This was Task 7 of [`plan-brave-librewolf.md`](plan-brave-librewolf.md). It is unchanged except where the server replaced the host manifests.

**Files:**
- Modify: `e2e/env.go`, `e2e/browser.go`, `e2e/bidi.go`, `e2e/install_test.go`, `README.md` (Development)

**Interfaces:**
- Consumes: Tasks 6–7 (`e.serve`, `connections`, `stopServer`'s message), and from the earlier plans `--profile`, `e.ready`, `chrome.ReadState`, `firefox.ReadState`.
- Produces: kinds `chrome`, `brave`, `firefox`, `librewolf` for `setupPair`; `bins`, `names`, `gecko`; `launch(t, name, kind, udd, env)`, `launchFirefox(t, name, kind, dir, port, env)`.

- [ ] **Step 1: The harness knows four kinds.** In `e2e/env.go`, add:

```go
// bins start each kind of browser in the dev image.
var bins = map[string]string{
	"chrome":    "google-chrome",
	"brave":     "brave-browser",
	"firefox":   "/opt/firefox-devedition/firefox",
	"librewolf": "/opt/librewolf/librewolf",
}

// names are how install's restart line names each kind.
var names = map[string]string{"chrome": "Chrome", "brave": "Brave", "firefox": "Firefox", "librewolf": "LibreWolf"}

// gecko reports whether kind is a Firefox-family browser.
func gecko(kind string) bool { return kind == "firefox" || kind == "librewolf" }
```

The `kinds` field comment: "browser name to its kind, a key of bins". In `open`, `if b.Kind == "firefox"` → `if gecko(b.Kind)`. `setupPair`'s comment: "…in browser kind ka and b in kb (keys of bins)…". `firstInstall` becomes:

```go
// firstInstall prepares both profiles and runs install's first pass, which
// writes the files and asks for a restart: once per Chromium kind, since
// its files reach every data directory, and once per Gecko profile, since
// each holds its own add-on and prefs.
func (e *Env) firstInstall(t *testing.T) {
	t.Helper()
	e.prepare(t, "a")
	e.prepare(t, "b")
	done := map[string]bool{}
	for _, name := range []string{"a", "b"} {
		k := e.kind(name)
		if !gecko(k) {
			if done[k] {
				continue
			}
			done[k] = true
			// Registered before install runs, so an install that fails
			// part-way leaves nothing behind either, and run before
			// t.TempDir's cleanup: later tests must not see this test's
			// extension in the shared system directories.
			t.Cleanup(func() { e.mm(t, "uninstall", "--browser", k) })
		}
		out := e.mm(t, e.installArgs(name)...)
		if !strings.Contains(out, "Restart "+names[k]) {
			t.Fatalf("first install did not ask for a restart:\n%s", out)
		}
		if !gecko(k) {
			m := extIDRe.FindStringSubmatch(out)
			if m == nil {
				t.Fatalf("first install did not print the extension id:\n%s", out)
			}
			if e.ExtID != "" && m[1] != e.ExtID {
				t.Fatalf("%s got extension id %s, the other browser %s", k, m[1], e.ExtID)
			}
			e.ExtID = m[1]
		}
	}
}
```

In `prepare`: `if gecko(e.kind(name))`, `exec.Command(bins[e.kind(name)], "--headless", "-CreateProfile", name+" "+dir)`, and "create %s profile %s" with the kind. `udd`:

```go
func (e *Env) udd(name string) string {
	k := e.kind(name)
	if gecko(k) {
		return filepath.Join(e.Home, k, name)
	}
	return filepath.Join(e.Home, k+"-"+name)
}
```

`launch`:

```go
func (e *Env) launch(name string) *Browser {
	if k := e.kind(name); gecko(k) {
		return launchFirefox(e.t, name, k, e.udd(name), map[string]int{"a": 9301, "b": 9302}[name], e.env)
	}
	return launch(e.t, name, e.kind(name), e.udd(name), e.env)
}
```

`installArgs`:

```go
func (e *Env) installArgs(name string) []string {
	if k := e.kind(name); gecko(k) {
		return []string{"install", "--browser", k, "--profile", name}
	}
	return []string{"install", "--browser", e.kind(name), "--user-data-dir", e.udd(name), "--profile", "Default"}
}
```

`bind`: `if gecko(e.kind(name)) { profile = name }`. A Gecko profile's ID is its directory's base name, `name`, because `prepare` creates it at `<Home>/<kind>/<name>`. `ready` reads the profile's files by family, with `if gecko(e.kind(name))` in place of `if e.kind(name) == "firefox"`.

In `e2e/browser.go`: `Kind string // a key of bins`; `func launch(t *testing.T, name, kind, udd string, env []string) *Browser` with `exec.Command(bins[kind], "--user-data-dir="+udd, …)` (flags unchanged; the comment on `--no-sandbox` now says "Chromium's sandbox") and `Kind: kind`. In `e2e/bidi.go`: delete `firefoxBin`; `func launchFirefox(t *testing.T, name, kind, dir string, port int, env []string) *Browser` with `exec.Command(bins[kind], "--profile", dir, …)` and `Kind: kind`.

- [ ] **Step 2: Check the refactor:** `bin/dev run make check-e2e RUN='TestInstallAndBind|TestFirefoxInstallAndBind'` → PASS.

- [ ] **Step 3: Write the tests** in `e2e/install_test.go` (imports: `internal/firefox`, `extension`). Where the old plan checked that the shared host manifest stayed, these check what the other browser of the family still needs from the uninstalled one: Chromium's CRX, and for both families the service and a round in the other browser.

```go
// TestBraveInstallAndBind: Brave installs the extension under Chrome's id
// and binds as Chrome does, and uninstalling Brave leaves Chrome syncing.
func TestBraveInstallAndBind(t *testing.T) {
	e := setupPair(t, "chrome", "brave")
	if !strings.HasPrefix(e.DBB, "brave-") {
		t.Fatalf("Brave bound to %s", e.DBB)
	}
	self := eval[struct {
		ID          string `json:"id"`
		InstallType string `json:"installType"`
		Enabled     bool   `json:"enabled"`
	}](t, e.B, `chrome.management.getSelf()`)
	if self.ID != e.ExtID || self.InstallType != "sideload" || !self.Enabled {
		t.Errorf("Brave: extension %+v, want id %s, sideload, enabled", self, e.ExtID)
	}
	d, err := db.LoadDB(e.Paths.ProfileFile(e.DBB))
	if err != nil || d.Profile.Browser != "brave" || d.Profile.Path != filepath.Join(e.B.UDD, "Default") ||
		d.Profile.Origin != "chrome-extension://"+e.ExtID {
		t.Errorf("%s database %+v, %v", e.DBB, d, err)
	}
	roundDone(t, e.A, "Chrome's round after binding")
	roundDone(t, e.B, "Brave's round after binding")

	e.B.Close()
	out := e.mm(t, "uninstall", "--browser", "brave")
	if _, err := os.Stat(chrome.Brave.ExternalFile(e.ExtID)); !os.IsNotExist(err) {
		t.Errorf("Brave's external file is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.Paths.Data, "mirrormark.crx")); err != nil {
		t.Fatalf("the CRX went with Brave: %v", err)
	}
	if !strings.Contains(out, "Kept the mirrormark service, which Chrome still uses.") {
		t.Errorf("uninstall did not keep the service for Chrome:\n%s", out)
	}
	eval[bool](t, e.A, `chrome.storage.local.remove("status").then(() => true)`)
	e.A.Add(t, "other", "Example", "https://example.com/")
	roundDone(t, e.A, "Chrome's round after Brave's uninstall")
}

// TestLibreWolfInstallAndBind: LibreWolf takes Developer Edition's route,
// and uninstalling it resets its prefs and leaves Developer Edition
// syncing.
func TestLibreWolfInstallAndBind(t *testing.T) {
	e := setupPair(t, "librewolf", "firefox")
	if e.DBA != "librewolf-a" || e.DBB != "firefox-b" {
		t.Fatalf("bound to %s and %s", e.DBA, e.DBB)
	}
	if got := eval[string](t, e.A, `chrome.runtime.id`); got != extension.GeckoID {
		t.Errorf("LibreWolf: extension id %q", got)
	}
	d, err := db.LoadDB(e.Paths.ProfileFile(e.DBA))
	if err != nil || d.Profile.Browser != "librewolf" || d.Profile.Path != e.A.UDD || !strings.HasPrefix(d.Profile.Origin, "moz-extension://") {
		t.Errorf("%s database %+v, %v", e.DBA, d, err)
	}
	roundDone(t, e.A, "LibreWolf's round after binding")
	roundDone(t, e.B, "Developer Edition's round after binding")

	e.A.Close()
	out := e.mm(t, "uninstall", "--browser", "librewolf")
	if _, err := os.Stat(firefox.XPIPath(e.udd("a"))); !os.IsNotExist(err) {
		t.Errorf("the XPI is still in LibreWolf's profile: %v", err)
	}
	if user, _ := os.ReadFile(filepath.Join(e.udd("a"), "user.js")); !bytes.Contains(user, []byte(`user_pref("xpinstall.signatures.required", true);`)) {
		t.Errorf("LibreWolf's user.js not put back:\n%s", user)
	}
	if !strings.Contains(out, "Kept the mirrormark service, which Firefox Developer Edition still uses.") {
		t.Errorf("uninstall did not keep the service for Developer Edition:\n%s", out)
	}
	eval[bool](t, e.B, `chrome.storage.local.remove("status").then(() => true)`)
	e.B.Add(t, "other", "Example", "https://example.com/")
	roundDone(t, e.B, "Developer Edition's round after LibreWolf's uninstall")
}
```

  The service lines: in the dev image `systemctl` is missing, so `install` printed the fallback and wrote the unit anyway. `stopServer` keeps the unit and prints the "Kept" line whatever systemd does. If the e2e `Home` has no unit because `startServer` failed before writing it, stop and report. It writes the unit before it calls `systemctl`.

- [ ] **Step 4: Run** `bin/dev run make check-e2e RUN='TestBraveInstallAndBind|TestLibreWolfInstallAndBind'` → PASS.
  - If LibreWolf's install says it found no profile `a`, check where `-CreateProfile` registered it: `bin/dev run sh -c 'export HOME=$(mktemp -d); /opt/librewolf/librewolf --headless -CreateProfile "a $HOME/p"; find $HOME -name profiles.ini'`. If it is neither `~/.config/librewolf/librewolf` nor `~/.librewolf`, stop and report: Section 9's profile root is then wrong.
  - If a LibreWolf profile's first start does not show the add-on ready within `e.ready`'s 40 s, or a ready profile does not report in within `chrome.BindWait` (30 s, one open), stop and report the time it took. Do not raise the waits silently, and do not add a retry. Gecko's first socket can drop about 10 s after start and wait 30 s for the alarm. If that pushes a bind past 30 s, report it with the log lines; the fix belongs in the RFC, not in a longer wait.
  - If Brave's `Preferences` does not show the extension within `e.ready`'s 40 s of the profile's start, record how long it took (the profile-state spike measured 10.3 s), and stop.
  - If `chrome.management.getSelf()` gives Brave another `installType`, record what it gives and assert that instead, with a comment.

- [ ] **Step 5: README Development:** "Besides installing and binding in each of the four browsers, …". Run `bin/dev run make && bin/dev run make check-e2e` → PASS.

- [ ] **Step 6: Commit** `test(rfc-0001): install and bind Brave and LibreWolf end to end`.

---

### Task 9: Run the sync tests between Brave and LibreWolf

This was Task 8 of [`plan-brave-librewolf.md`](plan-brave-librewolf.md), unchanged: the sync tests do not see the transport.

**Files:**
- Modify: `e2e/sync_test.go`, `README.md` (Development)

- [ ] **Step 1: The pair.**

```go
// pairs are the browsers of profiles a and b each sync test runs with. One
// cross-engine pair covers Brave and LibreWolf; the others add little
// beyond what their families' pairs show, and each pair adds minutes.
var pairs = [][2]string{{"chrome", "chrome"}, {"firefox", "firefox"}, {"firefox", "chrome"}, {"brave", "librewolf"}}
```

- [ ] **Step 2: Run** `bin/dev run make check-e2e RUN='/brave-librewolf'` → PASS, each latency under 3 s logged. Then the full `bin/dev run make check-e2e` → PASS. The suite grows by about a third and stays inside the 60 m timeout.

- [ ] **Step 3: README Development:** "The sync tests run four times: between two Chromes, between two Firefoxes, between a Firefox and a Chrome, and between Brave and LibreWolf, as subtests named `chrome-chrome`, `firefox-firefox`, `firefox-chrome` and `brave-librewolf` …".

- [ ] **Step 4: Commit** `test(rfc-0001): run the sync tests between Brave and LibreWolf`.

## After the last task

- In a `docs(rfc-0001)` commit, bring Section 11, Phase 8 and the History up to date with anything built differently from them, and the Brave and LibreWolf parts of Section 9 with Tasks 8–9. Move what Tasks 6, 8 and 9 checked into the spike's results: the real server in all four browsers, and the Gecko keep-alive under the e2e suite's lifetimes.
- `TASKS.md`:
  - Under "The host log grows within one session", say that a session is now the server's whole run, usually the user's login, which makes rotating on write more worth doing.
  - Under "Wrong-binding detection relies on browser ids not matching", note that the token now ties a connection to its database. It still does not tell a wrongly bound profile, so the entry stays.
  - Add that `e2e/bidi.go` could use `ws.Dial`, which has deadlines and handles control frames: this would fix "The BiDi handshake has no deadline" and "The BiDi client ignores control frames".
  - Under "Manual checks still to do", add: on the desktop, upgrade from the native-messaging build in Chrome and Developer Edition, following the README. Check that the first `install` asks for a restart and starts the service (`systemctl --user status mirrormark`), the second binds, the popup clears, and a server restart (`systemctl --user restart mirrormark`) and a browser restart each reconnect. Then run the Brave and LibreWolf steps from [`plan-brave-librewolf.md`](plan-brave-librewolf.md)'s "After the last task". Phase 8 is done when these runs are recorded.

## Risks

- **The two halves of the proof live in two languages.** Go computes it in `host.mac`, the extension in `mac` in `background.js`. Only the e2e suite runs both; `server_test.go` pins the Go half to the exact inputs, and every e2e binding fails with `bad-token` if the halves disagree.
- **The switch is one large commit** (Task 6). It touches the extension, the protocol, `install` and the e2e harness at once, because a smaller step would leave either the tests or the users without a transport. Review it in the order the steps give.
- **Gecko's first-socket drop** (spike, cause unknown) can add up to 30 s after a Gecko start before the first sync. The e2e waits that follow a Firefox start must allow for it; Task 6 Step 5 says what to do if one does not.
- **The ports in the dev image:** e2e tests run one at a time and each stops its server, so a fixed port is free for the next. A server left from a crashed run holds `61959`. The next test's server takes `63018`, and the extension skips the stale one, because the stale server does not know the new home's origins. This shows in the log as `refused a connection`.
- **`serverRunning` takes the serve lock for a moment.** A server that systemd starts in that instant exits and is restarted by `Restart=on-failure`. This is harmless, but it shows in the journal.
- **Not tested here, and listed in the RFC:** sleep and resume, a server down when the browser starts, Chrome and Brave with their own sandbox, a program holding all three ports, and an upgrade that changes only the binary.
