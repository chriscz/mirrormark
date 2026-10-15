package ws_test

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/ws"
)

// echo serves one connection: each text message comes back as it came.
// got receives Read's final error.
func echo(t *testing.T, max int) (addr string, got chan error) {
	got = make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Upgrade(w, r, max)
		if err != nil {
			got <- err
			return
		}
		for {
			m, err := c.Read()
			if err != nil {
				got <- err
				return
			}
			c.Write(m)
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), got
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
	if err := <-got; !errors.Is(err, io.EOF) {
		t.Fatalf("server Read after close: %v", err)
	}
}

// raw opens a connection and does the handshake by hand, for frames Dial
// would never send.
func raw(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	nc.SetDeadline(time.Now().Add(5 * time.Second)) // fail, not hang, if no close frame comes
	io.WriteString(nc, "GET / HTTP/1.1\r\nHost: "+addr+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(nc)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 101 {
		t.Fatalf("handshake: %v %v", resp, err)
	}
	// The example key and answer from the WebSocket specification.
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept %q", got)
	}
	return nc, br
}

// frame writes one frame with the given first byte, masked unless mask
// is false.
func frame(nc net.Conn, b0 byte, payload []byte, mask bool) {
	h := []byte{b0, byte(len(payload))}
	if mask {
		h[1] |= 0x80
		h = append(h, 1, 2, 3, 4)
		p := make([]byte, len(payload))
		for i := range payload {
			p[i] = payload[i] ^ []byte{1, 2, 3, 4}[i%4]
		}
		payload = p
	}
	nc.Write(append(h, payload...))
}

// closeCode reads the server's close frame and returns its code.
func closeCode(t *testing.T, br *bufio.Reader) uint16 {
	t.Helper()
	for {
		var h [2]byte
		if _, err := io.ReadFull(br, h[:]); err != nil {
			t.Fatal(err)
		}
		p := make([]byte, h[1]&0x7f)
		io.ReadFull(br, p)
		if h[0]&0x0f == 0x8 {
			return binary.BigEndian.Uint16(p)
		}
	}
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
			if err := <-got; err == nil || errors.Is(err, io.EOF) {
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
		var h [2]byte
		io.ReadFull(br, h[:])
		p := make([]byte, h[1]&0x7f)
		io.ReadFull(br, p)
		got = append(got, string([]byte{h[0]})+string(p))
	}
	if got[0] != "\x8ap" || got[1] != "\x81abcd" {
		t.Fatalf("got %q, want the pong, then the joined message", got)
	}
}

func TestRefusesAPlainRequest(t *testing.T) {
	addr, _ := echo(t, 1<<20)
	resp, err := http.Get("http://" + addr + "/")
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("%v %v", resp, err)
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
		{"length just over the limit", []byte{0x81, 0xfe, 0x00, 0x7e}, 1009},
		{"long ping", []byte{0x89, 0xfe, 0x00, 0x7e}, 1002},
		{"fragmented ping", []byte{0x09, 0x80, 1, 2, 3, 4}, 1002},
		{"text inside a fragmented text", []byte{0x01, 0x81, 0, 0, 0, 0, 'a', 0x81, 0x81, 0, 0, 0, 0, 'b'}, 1002},
		{"unknown opcode", []byte{0x83, 0x80, 1, 2, 3, 4}, 1003},
		{"bad UTF-8", []byte{0x81, 0x82, 0, 0, 0, 0, 0xc3, 0x28}, 1007},
	} {
		t.Run(c.name, func(t *testing.T) {
			addr, got := echo(t, 100)
			nc, br := raw(t, addr)
			nc.Write(c.raw)
			if code := closeCode(t, br); code != c.want {
				t.Errorf("close code %d, want %d", code, c.want)
			}
			if err := <-got; err == nil || errors.Is(err, io.EOF) {
				t.Errorf("server Read: %v", err)
			}
		})
	}
}

func TestReadEndsWithEOFWhenThePeerHangsUp(t *testing.T) {
	addr, got := echo(t, 1<<20)
	nc, _ := raw(t, addr)
	nc.Close()
	if err := <-got; !errors.Is(err, io.EOF) {
		t.Fatalf("server Read: %v", err)
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
