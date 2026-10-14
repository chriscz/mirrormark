//go:build e2e

// Package e2e drives two real Google Chrome instances through the DevTools
// protocol to test mirrormark from install to sync.
package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// cdp speaks the DevTools protocol over --remote-debugging-pipe: JSON
// messages ended by a NUL byte, commands on Chrome's fd 3, replies on fd 4.
type cdp struct {
	w    *os.File
	mu   sync.Mutex
	next int
	wait map[int]chan reply
	dead chan struct{}
}

type reply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type request[P any] struct {
	ID        int    `json:"id"`
	Method    string `json:"method"`
	SessionID string `json:"sessionId,omitempty"`
	Params    P      `json:"params"`
}

type none struct{}

func newCDP(w, r *os.File) *cdp {
	c := &cdp{w: w, wait: map[int]chan reply{}, dead: make(chan struct{})}
	go c.read(r)
	return c
}

// read delivers replies by id. Events carry no id; the tests need none.
func (c *cdp) read(r io.Reader) {
	defer close(c.dead)
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		b, err := br.ReadBytes(0)
		if err != nil {
			return
		}
		var m reply
		if json.Unmarshal(b[:len(b)-1], &m) != nil || m.ID == 0 {
			continue
		}
		c.mu.Lock()
		ch := c.wait[m.ID]
		delete(c.wait, m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	}
}

func call[P, R any](c *cdp, session, method string, params P) (R, error) {
	var out R
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan reply, 1)
	c.wait[id] = ch
	b, err := json.Marshal(request[P]{ID: id, Method: method, SessionID: session, Params: params})
	if err == nil {
		_, err = c.w.Write(append(b, 0))
	}
	c.mu.Unlock()
	if err != nil {
		return out, err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return out, fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		return out, json.Unmarshal(m.Result, &out)
	case <-c.dead:
		return out, fmt.Errorf("%s: Chrome closed the pipe", method)
	case <-time.After(30 * time.Second):
		return out, fmt.Errorf("%s: no reply within 30 s", method)
	}
}
