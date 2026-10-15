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

// Conn is one WebSocket connection. One goroutine may Read while others
// Write.
type Conn struct {
	c      net.Conn
	r      *bufio.Reader
	max    int
	client bool       // a client masks what it sends; a server must not
	mu     sync.Mutex // one frame at a time
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
// a handshake gets 400.
func Upgrade(w http.ResponseWriter, r *http.Request, max int) (*Conn, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if r.Method != http.MethodGet || !has(r.Header, "Connection", "upgrade") || !has(r.Header, "Upgrade", "websocket") ||
		r.Header.Get("Sec-WebSocket-Version") != "13" || key == "" {
		http.Error(w, "not a WebSocket handshake", http.StatusBadRequest)
		return nil, errors.New("not a WebSocket handshake")
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
// ends between frames.
func (c *Conn) Read() ([]byte, error) {
	var msg []byte
	inMsg := false
	for {
		fin, op, payload, err := c.frame()
		if err != nil {
			return nil, err
		}
		switch op {
		case opPing:
			if err := c.write(opPong, payload); err != nil {
				return nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			c.Close(1000, "") // answer the close, then hang up
			return nil, io.EOF
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
			return nil, c.fail(1003, "only text messages are accepted")
		}
		if len(msg)+len(payload) > c.max {
			return nil, c.fail(1009, "message too large")
		}
		msg = append(msg, payload...)
		if fin {
			if !utf8.Valid(msg) {
				return nil, c.fail(1007, "a text message that is not UTF-8")
			}
			return msg, nil
		}
	}
}

// frame reads one frame and unmasks it.
func (c *Conn) frame() (fin bool, op byte, payload []byte, err error) {
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
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.r, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.r, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if op >= opClose && (n > 125 || !fin) {
		err = c.fail(1002, "a control frame that is long or fragmented")
		return
	}
	if n > uint64(c.max) {
		err = c.fail(1009, "message too large")
		return
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.r, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(c.r, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}

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
// connection. Closing twice is harmless. The reason must be at most 123
// bytes, the room a control frame leaves after the code.
func (c *Conn) Close(code uint16, reason string) error {
	c.write(opClose, append(binary.BigEndian.AppendUint16(nil, code), reason...))
	return c.c.Close()
}

// fail closes the connection with code and returns why.
func (c *Conn) fail(code uint16, why string) error {
	c.Close(code, why)
	return fmt.Errorf("websocket: %s", why)
}
