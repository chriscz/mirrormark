//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/firefox"
)

// TestFirefoxSeparatorsStayPut: separators never reach Chrome, new
// bookmarks land where the host means despite them, and a purged folder
// that holds one goes.
func TestFirefoxSeparatorsStayPut(t *testing.T) {
	e := setupPair(t, "firefox", "chrome")
	e.Union(t)
	e.A.Add(t, work, "GitHub", gh)
	e.A.AddSeparator(t, work, 0)
	waitLines(t, e.B, work, []string{"/GitHub " + gh}, syncTimeout)
	e.B.Add(t, work, "News", lwn)
	waitLines(t, e.A, work, []string{"/GitHub " + gh, "/News " + lwn}, syncTimeout)
	if got := e.A.Separators(t, work); !slices.Equal(got, []int{0}) {
		t.Fatalf("separators at %v, want [0]", got)
	}

	e.A.Mkdir(t, work+"/Old")
	e.A.Add(t, work+"/Old", "Go", "https://go.dev/")
	e.A.AddSeparator(t, work+"/Old", -1)
	both(t, e, "/GitHub "+gh, "/News "+lwn, "/Old/", "/Old/Go https://go.dev/")
	e.B.Remove(t, work+"/Old/")
	both(t, e, "/GitHub "+gh, "/News "+lwn, "/_deleted/", "/_deleted/Old/", "/_deleted/Old/Go https://go.dev/")
	e.B.Remove(t, work+"/_deleted/Old/") // a purge: A must remove a folder holding a separator
	settled(t, e, nil, "/GitHub "+gh, "/News "+lwn, "/_deleted/")
}

// TestFirefoxUninstall: the add-on goes and the profile checks signatures
// again.
func TestFirefoxUninstall(t *testing.T) {
	e := setupPair(t, "firefox", "firefox")
	e.A.Close()
	e.B.Close()
	e.mm(t, "uninstall", "--browser", "firefox")
	if _, err := os.Stat(firefox.XPIPath(e.udd("a"))); !os.IsNotExist(err) {
		t.Fatalf("the XPI is still in profile a: %v", err)
	}
	userJS, err := os.ReadFile(filepath.Join(e.udd("a"), "user.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`user_pref("xpinstall.signatures.required", true);`,
		`user_pref("extensions.autoDisableScopes", 15);`,
	} {
		if !bytes.Contains(userJS, []byte(line)) {
			t.Errorf("user.js lacks %s:\n%s", line, userJS)
		}
	}
	e.A = e.launch("a")
	e.A.Close() // Firefox records the add-on's removal by the time it exits
	data, err := os.ReadFile(filepath.Join(e.udd("a"), "extensions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var x struct {
		Addons []struct {
			ID     string `json:"id"`
			Active bool   `json:"active"`
		} `json:"addons"`
	}
	if err := json.Unmarshal(data, &x); err != nil {
		t.Fatal(err)
	}
	for _, a := range x.Addons {
		if a.ID == extension.GeckoID && a.Active {
			t.Fatal("mirrormark is still active")
		}
	}
	if prefs, _ := os.ReadFile(filepath.Join(e.udd("a"), "prefs.js")); bytes.Contains(prefs, []byte(`"xpinstall.signatures.required", false`)) {
		t.Fatal("prefs.js still turns signature checks off")
	}
}
