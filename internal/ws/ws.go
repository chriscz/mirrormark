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
