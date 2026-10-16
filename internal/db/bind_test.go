package db_test

import (
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/chriscz/mirrormark/internal/db"
)

func TestBindWritesTheOriginAndKeepsTheToken(t *testing.T) {
	d := t.TempDir()
	p := db.Paths{Config: d + "/c", Data: d + "/d", State: d + "/s"}
	if err := db.SaveDB(p.ProfileFile("a"), &db.DB{Format: 1, Profile: db.Profile{ID: "a", Label: "x/a", Browser: "firefox"}}); err != nil {
		t.Fatal(err)
	}
	tok, err := db.Bind(p, "a", "moz-extension://u1")
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(tok) {
		t.Fatalf("token %q, %v", tok, err)
	}
	again, err := db.Bind(p, "a", "moz-extension://u2")
	if err != nil || again != tok {
		t.Fatalf("second bind gave %q, %v; want the same token", again, err)
	}
	got, _ := db.LoadDB(p.ProfileFile("a"))
	if got.Profile.Origin != "moz-extension://u2" || got.Profile.Token != tok {
		t.Fatalf("profile %+v", got.Profile)
	}
}

func bindPaths(t *testing.T) db.Paths {
	d := t.TempDir()
	p := db.Paths{Config: d + "/c", Data: d + "/d", State: d + "/s"}
	if err := db.SaveDB(p.ProfileFile("a"), &db.DB{Format: 1, Profile: db.Profile{ID: "a", Label: "x/a", Browser: "firefox"}}); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBindReplacesAMalformedToken(t *testing.T) {
	p := bindPaths(t)
	d, _ := db.LoadDB(p.ProfileFile("a"))
	d.Profile.Origin = "moz-extension://u1"
	d.Profile.Token = "NOT-HEX"
	if err := db.SaveDB(p.ProfileFile("a"), d); err != nil {
		t.Fatal(err)
	}
	tok, err := db.Bind(p, "a", "moz-extension://u1")
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(tok) {
		t.Fatalf("token %q, %v", tok, err)
	}
	got, _ := db.LoadDB(p.ProfileFile("a"))
	if got.Profile.Token != tok {
		t.Fatalf("file holds %q, want %q", got.Profile.Token, tok)
	}
}

func TestBindLeavesTheFileAloneWhenNothingChanges(t *testing.T) {
	p := bindPaths(t)
	if _, err := db.Bind(p, "a", "moz-extension://u1"); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p.ProfileFile("a"), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Bind(p, "a", "moz-extension://u1"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p.ProfileFile("a"))
	if err != nil || st.ModTime().After(old.Add(time.Minute)) {
		t.Fatalf("file was rewritten: %v, %v", st.ModTime(), err)
	}
}

func TestBindKeepsTheFileMode(t *testing.T) {
	p := bindPaths(t)
	if _, err := db.Bind(p, "a", "moz-extension://u1"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p.ProfileFile("a"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, %v", st.Mode(), err)
	}
}
