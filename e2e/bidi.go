//go:build e2e

package e2e

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"
)

// bidi speaks WebDriver BiDi to Firefox over its WebSocket on
// 127.0.0.1:<port>/session: masked text frames to Firefox, unmasked and
// possibly fragmented frames back. One command at a time; events are
// skipped, as the tests need none.
type bidi struct {
	conn net.Conn
	r    *bufio.Reader
	mu   sync.Mutex
	next int
}

func dialBiDi(port int, within time.Duration) (*bidi, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(within)
	for {
		b, err := handshake(addr)
		if err == nil {
			return b, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("BiDi on %s: %w", addr, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func handshake(addr string) (*bidi, error) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 16)
	rand.Read(key)
	fmt.Fprintf(c, "GET /session HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		addr, base64.StdEncoding.EncodeToString(key))
	r := bufio.NewReader(c)
	resp, err := http.ReadResponse(r, nil)
	if err == nil && resp.StatusCode != http.StatusSwitchingProtocols {
		err = fmt.Errorf("handshake: %s", resp.Status)
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return &bidi{conn: c, r: r}, nil
}

func (b *bidi) send(p []byte) error {
	h := []byte{0x81} // final text frame
	switch n := len(p); {
	case n < 126:
		h = append(h, 0x80|byte(n))
	case n < 1<<16:
		h = append(h, 0x80|126, byte(n>>8), byte(n))
	default:
		h = binary.BigEndian.AppendUint64(append(h, 0x80|127), uint64(n))
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	h = append(h, mask...)
	for i, c := range p {
		h = append(h, c^mask[i%4])
	}
	_, err := b.conn.Write(h)
	return err
}

func (b *bidi) recv() ([]byte, error) {
	var msg []byte
	for {
		var h [2]byte
		if _, err := io.ReadFull(b.r, h[:]); err != nil {
			return nil, err
		}
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var l [2]byte
			if _, err := io.ReadFull(b.r, l[:]); err != nil {
				return nil, err
			}
			n = uint64(binary.BigEndian.Uint16(l[:]))
		case 127:
			var l [8]byte
			if _, err := io.ReadFull(b.r, l[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint64(l[:])
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(b.r, p); err != nil {
			return nil, err
		}
		msg = append(msg, p...)
		if h[0]&0x80 != 0 {
			return msg, nil
		}
	}
}

type bidiReply struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	Result  json.RawMessage `json:"result"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

func bidiCall[P, R any](b *bidi, method string, params P) (R, error) {
	var out R
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	msg, err := json.Marshal(struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
		Params P      `json:"params"`
	}{b.next, method, params})
	if err != nil {
		return out, err
	}
	b.conn.SetDeadline(time.Now().Add(30 * time.Second))
	if err := b.send(msg); err != nil {
		return out, fmt.Errorf("%s: %w", method, err)
	}
	for {
		m, err := b.recv()
		if err != nil {
			return out, fmt.Errorf("%s: %w", method, err)
		}
		var r bidiReply
		if json.Unmarshal(m, &r) != nil || r.ID != b.next {
			continue // an event
		}
		if r.Type == "error" {
			return out, fmt.Errorf("%s: %s: %s", method, r.Error, r.Message)
		}
		return out, json.Unmarshal(r.Result, &out)
	}
}

type firefoxDriver struct {
	c   *bidi
	ctx string // the tab the tests use
}

type bidiTarget struct {
	Context string `json:"context"`
}

// eval returns the expression's value through JSON.stringify: BiDi hands
// back typed remote values, not JSON.
func (d *firefoxDriver) eval(expr string) (json.RawMessage, error) {
	type params struct {
		Expression   string     `json:"expression"`
		Target       bidiTarget `json:"target"`
		AwaitPromise bool       `json:"awaitPromise"`
	}
	r, err := bidiCall[params, struct {
		Type   string `json:"type"`
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}](d.c, "script.evaluate", params{"(async () => JSON.stringify(await " + expr + "))()", bidiTarget{d.ctx}, true})
	if err != nil {
		return nil, err
	}
	if r.Type == "exception" {
		return nil, errors.New(r.ExceptionDetails.Text)
	}
	if r.Result.Type != "string" {
		return nil, nil // undefined
	}
	var s string
	err = json.Unmarshal(r.Result.Value, &s)
	return json.RawMessage(s), err
}

func (d *firefoxDriver) openPage(url string) error {
	type params struct {
		Context string `json:"context"`
		URL     string `json:"url"`
		Wait    string `json:"wait"`
	}
	_, err := bidiCall[params, json.RawMessage](d.c, "browsingContext.navigate", params{d.ctx, url, "complete"})
	return err
}

func (d *firefoxDriver) close() { bidiCall[none, json.RawMessage](d.c, "browser.close", none{}) }

// launchFirefox starts Firefox Developer Edition in profile dir with BiDi
// on port, under the test's Xvfb and D-Bus, not headless: a headless
// Firefox does not take a second invocation's URL, which install's binding
// of a running profile needs.
func launchFirefox(t *testing.T, name, kind, dir string, port int, env []string) *Browser {
	t.Helper()
	logf, err := os.OpenFile(dir+".log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bins[kind], "--profile", dir, "--remote-debugging-port", strconv.Itoa(port), "about:blank")
	// The remote agent navigates only to http(s), file, blob and
	// about:blank unless system access is allowed; the tests' page is a
	// moz-extension one.
	cmd.Env, cmd.Stdout, cmd.Stderr = append(env[:len(env):len(env)], "MOZ_REMOTE_ALLOW_SYSTEM_ACCESS=1"), logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	b := &Browser{Name: name, Kind: kind, UDD: dir, cmd: cmd, done: make(chan struct{})}
	go func() {
		cmd.Wait()
		logf.Close()
		close(b.done)
	}()
	// A Firefox the test cannot drive is killed, not left running.
	fail := func(err error) {
		t.Helper()
		cmd.Process.Kill()
		<-b.done
		t.Fatalf("%s: %v", name, err)
	}
	c, err := dialBiDi(port, 30*time.Second)
	if err != nil {
		fail(err)
	}
	type caps struct {
		Capabilities struct{} `json:"capabilities"`
	}
	if _, err := bidiCall[caps, json.RawMessage](c, "session.new", caps{}); err != nil {
		fail(err)
	}
	type treeParams struct {
		MaxDepth int `json:"maxDepth"`
	}
	tree, err := bidiCall[treeParams, struct {
		Contexts []bidiTarget `json:"contexts"`
	}](c, "browsingContext.getTree", treeParams{0})
	if err == nil && len(tree.Contexts) == 0 {
		err = errors.New("no tab")
	}
	if err != nil {
		fail(err)
	}
	b.drv = &firefoxDriver{c: c, ctx: tree.Contexts[0].Context}
	t.Cleanup(b.Close)
	return b
}
