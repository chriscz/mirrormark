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

func TestWriteAtomicFailureKeepsTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.yaml")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(target, []byte("new"), 0o600); err == nil {
		t.Fatal("rename over a directory succeeded")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "f.yaml" {
		t.Fatalf("temp file left behind: %v", entries)
	}
	if got, err := os.ReadFile(filepath.Join(target, "keep")); err != nil || string(got) != "old" {
		t.Fatalf("existing content changed: %q, %v", got, err)
	}
}

func TestLoadDBRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.yaml")
	os.WriteFile(path, []byte("format: 1\nprofile: {id: x}\nunoins: {}\n"), 0o600)
	if _, err := LoadDB(path); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestBackupSameDayKeepsFirstCopy(t *testing.T) {
	p := Paths{Data: t.TempDir()}
	d := sample()
	if err := SaveDB(p.ProfileFile("chrome-a"), d); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	if err := Backup(p, "chrome-a", day); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(p.ProfileFile("chrome-a"))
	d.Profile.Label = "chrome/changed"
	if err := SaveDB(p.ProfileFile("chrome-a"), d); err != nil {
		t.Fatal(err)
	}
	if err := Backup(p, "chrome-a", day.Add(8*time.Hour)); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(p.BackupDir("chrome-a"), "2026-01-01.yaml")
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(first) {
		t.Fatalf("second backup overwrote the first:\n%s", got)
	}
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v, want 0600", info.Mode().Perm())
	}
}

func TestBackupDeletesOnlyItsOwnFiles(t *testing.T) {
	p := Paths{Data: t.TempDir()}
	if err := SaveDB(p.ProfileFile("chrome-a"), sample()); err != nil {
		t.Fatal(err)
	}
	dir := p.BackupDir("chrome-a")
	os.MkdirAll(dir, 0o700)
	others := []string{"0000-notes.yaml", "1999-01-01.yaml.bak", "README", "a.yaml"}
	for _, name := range others {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("mine"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range 32 {
		if err := Backup(p, "chrome-a", day.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range others {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was deleted: %v", name, err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 30+len(others) {
		t.Fatalf("got %d entries, want %d", len(entries), 30+len(others))
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-01-03.yaml")); err != nil {
		t.Fatalf("oldest kept backup missing: %v", err)
	}
}

func TestBackupReportsRotationErrors(t *testing.T) {
	p := Paths{Data: t.TempDir()}
	if err := SaveDB(p.ProfileFile("chrome-a"), sample()); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range 30 {
		if err := Backup(p, "chrome-a", day.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	// A directory with a backup's name cannot be removed with os.Remove.
	stuck := filepath.Join(p.BackupDir("chrome-a"), "2025-12-31.yaml")
	if err := os.MkdirAll(filepath.Join(stuck, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Backup(p, "chrome-a", day.AddDate(0, 0, 30)); err == nil {
		t.Fatal("rotation error not reported")
	}
}

func TestDataDirWithGlobCharacters(t *testing.T) {
	p := Paths{Data: filepath.Join(t.TempDir(), "a[b]*c?")}
	if err := SaveDB(p.ProfileFile("chrome-a"), sample()); err != nil {
		t.Fatal(err)
	}
	dbs, errs := LoadAll(p)
	if dbs["chrome-a"] == nil || len(errs) != 0 {
		t.Fatalf("dbs %v errs %v", dbs, errs)
	}
	day := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range 32 {
		if err := Backup(p, "chrome-a", day.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(p.BackupDir("chrome-a"))
	if len(entries) != 30 {
		t.Fatalf("got %d backups, want 30", len(entries))
	}
}

func TestLoadAllMissingDirIsEmpty(t *testing.T) {
	dbs, errs := LoadAll(Paths{Data: t.TempDir()})
	if len(dbs) != 0 || len(errs) != 0 {
		t.Fatalf("dbs %v errs %v", dbs, errs)
	}
}

func TestLoadAllReportsUnreadableDir(t *testing.T) {
	p := Paths{Data: t.TempDir()}
	// A file where the profiles directory should be cannot be listed.
	if err := os.WriteFile(p.ProfilesDir(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, errs := LoadAll(p)
	if len(errs) != 1 || errs[p.ProfilesDir()] == nil {
		t.Fatalf("errs %v, want one error for the profiles directory", errs)
	}
}

func TestLoadAllRejectsMismatchedID(t *testing.T) {
	p := Paths{Data: t.TempDir()}
	SaveDB(p.ProfileFile("chrome-a"), sample())
	raw, _ := os.ReadFile(p.ProfileFile("chrome-a"))
	os.WriteFile(p.ProfileFile("chrome-b"), raw, 0o600) // a copy still says chrome-a
	os.WriteFile(filepath.Join(p.ProfilesDir(), "Chrome_C.yaml"), raw, 0o600)
	dbs, errs := LoadAll(p)
	if dbs["chrome-a"] == nil || len(dbs) != 1 {
		t.Fatalf("dbs %v", dbs)
	}
	if err := errs["chrome-b"]; err == nil || !strings.Contains(err.Error(), "chrome-a") {
		t.Fatalf("copied file: %v", err)
	}
	if err := errs["Chrome_C"]; err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("bad file name: %v", err)
	}
}
