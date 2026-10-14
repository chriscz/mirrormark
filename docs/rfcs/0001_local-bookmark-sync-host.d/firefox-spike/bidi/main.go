// Minimal stdlib WebDriver BiDi client: bidi <port> <scope:chrome|content> <js-expression>...
// Each expression is evaluated (awaitPromise) in the chrome (parent process) context and the JSON result printed.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

type ws struct {
	c net.Conn
	r *bufio.Reader
}

func dial(addr, path string) (*ws, error) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	k := make([]byte, 16)
	rand.Read(k)
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, addr, base64.StdEncoding.EncodeToString(k))
	r := bufio.NewReader(c)
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 101 {
		return nil, fmt.Errorf("handshake: %s", resp.Status)
	}
	return &ws{c, r}, nil
}

func (w *ws) send(b []byte) error {
	h := []byte{0x81}
	n := len(b)
	switch {
	case n < 126:
		h = append(h, 0x80|byte(n))
	case n < 65536:
		h = append(h, 0x80|126, byte(n>>8), byte(n))
	default:
		h = append(h, 0x80|127)
		h = binary.BigEndian.AppendUint64(h, uint64(n))
	}
	m := make([]byte, 4)
	rand.Read(m)
	h = append(h, m...)
	p := make([]byte, n)
	for i := range b {
		p[i] = b[i] ^ m[i%4]
	}
	_, err := w.c.Write(append(h, p...))
	return err
}

func (w *ws) recv() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.r, h[:]); err != nil {
			return nil, err
		}
		n := uint64(h[1] & 0x7f)
		if n == 126 {
			var b [2]byte
			io.ReadFull(w.r, b[:])
			n = uint64(binary.BigEndian.Uint16(b[:]))
		} else if n == 127 {
			var b [8]byte
			io.ReadFull(w.r, b[:])
			n = binary.BigEndian.Uint64(b[:])
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(w.r, p); err != nil {
			return nil, err
		}
		msg = append(msg, p...)
		if h[0]&0x80 != 0 {
			return msg, nil
		}
	}
}

var id = 0

func call(w *ws, method string, params any) (json.RawMessage, error) {
	id++
	b, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := w.send(b); err != nil {
		return nil, err
	}
	for {
		m, err := w.recv()
		if err != nil {
			return nil, err
		}
		var r struct {
			ID      int             `json:"id"`
			Type    string          `json:"type"`
			Result  json.RawMessage `json:"result"`
			Error   string          `json:"error"`
			Message string          `json:"message"`
		}
		json.Unmarshal(m, &r)
		if r.ID != id {
			continue
		}
		if r.Type == "error" {
			return nil, fmt.Errorf("%s: %s", r.Error, r.Message)
		}
		return r.Result, nil
	}
}

func main() {
	if os.Args[1] == "cdp" {
		cdpMain()
		return
	}
	addr := "127.0.0.1:" + os.Args[1]
	var w *ws
	var err error
	for range 100 {
		if w, err = dial(addr, "/session"); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(1)
	}
	if _, err := call(w, "session.new", map[string]any{"capabilities": map[string]any{}}); err != nil {
		fmt.Println("session.new:", err)
		os.Exit(1)
	}
	tp := map[string]any{"maxDepth": 0}
	if os.Args[2] == "chrome" {
		tp["moz:scope"] = "chrome"
	}
	tree, err := call(w, "browsingContext.getTree", tp)
	if err != nil {
		fmt.Println("getTree:", err)
		os.Exit(1)
	}
	var t struct {
		Contexts []struct{ Context, URL string } `json:"contexts"`
	}
	json.Unmarshal(tree, &t)
	ctx := t.Contexts[0].Context
	for _, expr := range os.Args[3:] {
		if len(expr) > 4 && expr[:4] == "NAV " {
			r, err := call(w, "browsingContext.navigate", map[string]any{"context": ctx, "url": expr[4:], "wait": "complete"})
			fmt.Println("navigate:", string(r), err)
			continue
		}
		r, err := call(w, "script.evaluate", map[string]any{"expression": "(async () => JSON.stringify(await (" + expr + ")))()", "target": map[string]any{"context": ctx}, "awaitPromise": true})
		if err != nil {
			fmt.Println("ERR", err)
			continue
		}
		var v struct {
			Type             string                `json:"type"`
			Result           struct{ Value any }   `json:"result"`
			ExceptionDetails struct{ Text string } `json:"exceptionDetails"`
		}
		json.Unmarshal(r, &v)
		if v.Type == "exception" {
			fmt.Println("EXC", v.ExceptionDetails.Text)
		} else {
			fmt.Println(v.Result.Value)
		}
	}
	call(w, "session.end", map[string]any{})
}

// cdp <ws-url-path-from-/json> <expr>: evaluate in a page target via CDP.
func cdpMain() {
	addr := "127.0.0.1:" + os.Args[2]
	var w *ws
	var err error
	for range 100 {
		if w, err = dial(addr, os.Args[3]); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		fmt.Println("dial:", err)
		os.Exit(1)
	}
	for _, expr := range os.Args[4:] {
		id++
		b, _ := json.Marshal(map[string]any{"id": id, "method": "Runtime.evaluate", "params": map[string]any{"expression": "(async () => JSON.stringify(await (" + expr + ")))()", "awaitPromise": true, "returnByValue": true}})
		w.send(b)
		for {
			m, _ := w.recv()
			var r struct{ ID int }
			json.Unmarshal(m, &r)
			if r.ID == id {
				fmt.Println(string(m))
				break
			}
		}
	}
}
