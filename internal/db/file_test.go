package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() *DB {
	return &DB{
		Format:  1,
		Profile: Profile{ID: "chrome-a", Label: "chrome/a", Browser: "chrome", Path: "/u/.config/google-chrome/Default"},
		Unions: map[string]*Member{"work": {Seq: 3, Path: "toolbar/Work", ID: "58", Children: []*Node{
			{Title: "GitHub", URL: "https://github.com/", ID: "57", M: "m-1", By: "chrome/a"},
			{Title: "_deleted", ID: "90", M: "m-2", By: "chrome/a", Children: []*Node{
				{Title: "Old (chrome/b)", Suffix: "chrome/b", URL: "https://old/", ID: "88", M: "m-3", By: "chrome/b"},
			}},
		}}},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles", "chrome-a.yaml")
	if err := SaveDB(path, sample()); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDB(path)
	if err != nil {
		t.Fatal(err)
	}
	want := sample()
	if got.Profile != want.Profile || got.Unions["work"].Seq != 3 || !EqualNodes(got.Unions["work"].Children, want.Unions["work"].Children) {
		t.Fatalf("round trip changed the file: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw), "# Written by mirrormark.") {
		t.Fatalf("missing header: %q", raw[:40])
	}
}

func TestWriteAtomicLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		if err := WriteAtomic(filepath.Join(dir, "f.yaml"), []byte{byte('a' + i)}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
}

func TestLoadDBRejectsUnknownFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.yaml")
	os.WriteFile(path, []byte("format: 2\nprofile: {id: x}\n"), 0o600)
	if _, err := LoadDB(path); err == nil {
		t.Fatal("format 2 accepted")
	}
}

func TestBackupOncePerDayKeeps30(t *testing.T) {
	p := Paths{Data: t.TempDir()}
	if err := SaveDB(p.ProfileFile("chrome-a"), sample()); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range 35 {
		for range 2 { // a second call on the same day does nothing
			if err := Backup(p, "chrome-a", day.AddDate(0, 0, i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	entries, _ := os.ReadDir(p.BackupDir("chrome-a"))
	if len(entries) != 30 {
		t.Fatalf("got %d backups, want 30", len(entries))
	}
	if entries[0].Name() != "2026-01-06.yaml" {
		t.Fatalf("oldest kept is %s, want 2026-01-06.yaml", entries[0].Name())
	}
}

func TestLoadAllReportsBadFiles(t *testing.T) {
	p := Paths{Data: t.TempDir()}
	SaveDB(p.ProfileFile("chrome-a"), sample())
	os.WriteFile(p.ProfileFile("chrome-b"), []byte("{not yaml"), 0o600)
	dbs, errs := LoadAll(p)
	if dbs["chrome-a"] == nil || errs["chrome-b"] == nil || len(dbs) != 1 {
		t.Fatalf("dbs %v errs %v", dbs, errs)
	}
}
