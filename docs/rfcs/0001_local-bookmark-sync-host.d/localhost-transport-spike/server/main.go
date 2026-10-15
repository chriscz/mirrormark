// Spike server: a stdlib WebSocket echo server on 127.0.0.1 that logs
// every request's Origin, plus a page server for the web-page check.
//
//	server -addr 127.0.0.1:8765 -page 0.0.0.0:8001 -ping 20s -log /out/server.log
//
// /ws      WebSocket; logs open, every message and close; with -ping > 0
//
//	sends "ping N" every interval, otherwise sends nothing.
//
// /probe   plain GET, no CORS headers; logs the Origin.
// /log     POST, body logged (extension reports through no-cors fetch).
// -tls ADDR serves the same pages over HTTPS with a fresh self-signed
// certificate, for a secure-context page.
// page server: /page.html tries ws and fetch to -addr and reports to its
// own origin's /report.
package main

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

var (
	addr  = flag.String("addr", "127.0.0.1:8765", "API address")
	page  = flag.String("page", "0.0.0.0:8001", "page address")
	ping  = flag.Duration("ping", 0, "server ping interval, 0 = silent")
	logf  = flag.String("log", "", "log file (JSON lines)")
	tlsa  = flag.String("tls", "", "HTTPS page address, empty = none")
	start = time.Now()
	conns atomic.Int64
	out   io.Writer = os.Stdout
)

func emit(kind string, kv map[string]any) {
	kv["t"] = time.Now().Format("15:04:05.000")
	kv["kind"] = kind
	b, _ := json.Marshal(kv)
	fmt.Fprintln(out, string(b))
}

func hdr(r *http.Request) map[string]any {
	return map[string]any{"path": r.URL.String(), "origin": r.Header.Get("Origin"),
		"ua": r.Header.Get("User-Agent"), "remote": r.RemoteAddr,
		"secfetchsite": r.Header.Get("Sec-Fetch-Site"), "secfetchmode": r.Header.Get("Sec-Fetch-Mode")}
}

func main() {
	flag.Parse()
	if *logf != "" {
		f, err := os.OpenFile(*logf, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o666)
		if err != nil {
			log.Fatal(err)
		}
		out = f
	}
	emit("server-start", map[string]any{"addr": *addr, "ping": ping.String(), "pid": os.Getpid()})
	api := http.NewServeMux()
	api.HandleFunc("/ws", wsHandler)
	api.HandleFunc("/probe", func(w http.ResponseWriter, r *http.Request) {
		emit("probe", hdr(r))
		io.WriteString(w, "probe-ok")
	})
	api.HandleFunc("/log", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		m := hdr(r)
		m["body"] = string(b)
		emit("log", m)
	})
	pg := http.NewServeMux()
	pg.HandleFunc("/page.html", func(w http.ResponseWriter, r *http.Request) {
		emit("page", hdr(r))
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, strings.ReplaceAll(pageHTML, "API", *addr))
	})
	pg.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		m := hdr(r)
		m["body"] = string(b)
		emit("page-report", m)
	})
	go func() { log.Fatal(http.ListenAndServe(*page, pg)) }()
	if *tlsa != "" {
		go func() {
			srv := &http.Server{Addr: *tlsa, Handler: pg, TLSConfig: &tls.Config{Certificates: []tls.Certificate{selfSigned()}}}
			log.Fatal(srv.ListenAndServeTLS("", ""))
		}()
	}
	log.Fatal(http.ListenAndServe(*addr, api))
}

const pageHTML = `<!doctype html><title>page</title><pre id=o></pre><script>
const rep = (k, v) => { document.getElementById("o").textContent += k + " " + JSON.stringify(v) + "\n";
  navigator.sendBeacon("/report", JSON.stringify({k, v, href: location.href})); };
rep("loaded", navigator.userAgent);
fetch("http://API/probe").then(r => r.text()).then(t => rep("fetch-ok", t), e => rep("fetch-err", String(e)));
fetch("http://API/probe", {mode: "no-cors"}).then(r => rep("nocors-done", r.type), e => rep("nocors-err", String(e)));
try { const ws = new WebSocket("ws://API/ws?from=page");
  ws.onopen = () => { rep("ws-open", 1); ws.send("hello from page " + location.href); };
  ws.onmessage = (m) => rep("ws-msg", m.data);
  ws.onerror = () => rep("ws-error", 1);
  ws.onclose = (e) => rep("ws-close", e.code);
} catch (e) { rep("ws-throw", String(e)); }
setTimeout(() => rep("after10s", 1), 10000);
</script>`

func wsHandler(w http.ResponseWriter, r *http.Request) {
	m := hdr(r)
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		m["err"] = "not an upgrade"
		emit("ws-bad", m)
		http.Error(w, "upgrade required", 426)
		return
	}
	id := conns.Add(1)
	m["conn"] = id
	h := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	c, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer c.Close()
	fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
		base64.StdEncoding.EncodeToString(h[:]))
	rw.Flush()
	emit("ws-open", m)
	opened := time.Now()
	done := make(chan struct{})
	send := make(chan []byte, 8)
	go func() { // writer
		var t <-chan time.Time
		if *ping > 0 {
			tk := time.NewTicker(*ping)
			defer tk.Stop()
			t = tk.C
		}
		n := 0
		for {
			select {
			case <-done:
				return
			case b := <-send:
				writeFrame(c, 1, b)
			case <-t:
				n++
				if writeFrame(c, 1, []byte(fmt.Sprintf("ping %d", n))) != nil {
					return
				}
			}
		}
	}()
	why := readLoop(id, bufio.NewReader(rw), send)
	close(done)
	emit("ws-close", map[string]any{"conn": id, "why": why, "after_s": int(time.Since(opened).Seconds())})
}

func readLoop(id int64, br *bufio.Reader, send chan<- []byte) string {
	for {
		var h [2]byte
		if _, err := io.ReadFull(br, h[:]); err != nil {
			return "read: " + err.Error()
		}
		op := h[0] & 0x0f
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			io.ReadFull(br, b[:])
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			io.ReadFull(br, b[:])
			n = binary.BigEndian.Uint64(b[:])
		}
		var mask [4]byte
		if h[1]&0x80 != 0 {
			io.ReadFull(br, mask[:])
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(br, p); err != nil {
			return "read: " + err.Error()
		}
		for i := range p {
			p[i] ^= mask[i%4]
		}
		switch op {
		case 8:
			code := 0
			if len(p) >= 2 {
				code = int(binary.BigEndian.Uint16(p))
			}
			return fmt.Sprintf("close frame %d", code)
		case 9:
			continue // pong ignored; browsers do not send pings
		case 1, 2:
			emit("ws-msg", map[string]any{"conn": id, "msg": string(p)})
			if strings.HasPrefix(string(p), "echo ") {
				send <- p
			}
		}
	}
}

func writeFrame(c net.Conn, op byte, p []byte) error {
	h := []byte{0x80 | op}
	switch {
	case len(p) < 126:
		h = append(h, byte(len(p)))
	case len(p) < 65536:
		h = append(h, 126, byte(len(p)>>8), byte(len(p)))
	default:
		h = append(h, 127)
		h = binary.BigEndian.AppendUint64(h, uint64(len(p)))
	}
	_, err := c.Write(append(h, p...))
	return err
}

func selfSigned() tls.Certificate {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	t := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(24 * time.Hour), IPAddresses: []net.IP{net.ParseIP("203.0.113.5")}}
	der, _ := x509.CreateCertificate(rand.Reader, t, t, &k.PublicKey, k)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}
}
