package db_test

import (
	"regexp"
	"testing"

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
