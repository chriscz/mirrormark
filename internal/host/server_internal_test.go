package host

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/ws"
)

// A Read that the server's own close cuts short ends the connection
// cleanly, as the extension going away does, rather than as a failure.
func TestWSConnReadAfterOwnCloseIsEOF(t *testing.T) {
	got := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sock, err := ws.Upgrade(w, r, maxIn)
		if err != nil {
			got <- err
			return
		}
		c := newWSConn(sock, time.Hour)
		read := make(chan error, 1)
		go func() {
			_, err := c.Read()
			read <- err
		}()
		time.Sleep(50 * time.Millisecond) // let Read block
		c.close()
		got <- <-read
	}))
	defer srv.Close()
	// The client never answers the close, so the server's Read sees its
	// own socket closed rather than the peer's close frame.
	client, err := ws.Dial(strings.TrimPrefix(srv.URL, "http://"), nil, maxIn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(1000, "")
	select {
	case err := <-got:
		if err != io.EOF {
			t.Fatalf("Read after close: %v, want io.EOF", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read did not return after close")
	}
}
