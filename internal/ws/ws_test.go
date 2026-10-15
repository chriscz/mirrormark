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
