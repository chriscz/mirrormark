package db

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTryLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	unlock, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock: %v, want ErrLocked", err)
	}
	unlock()
	unlock2, err := TryLock(path)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	unlock2()
}

func TestConnected(t *testing.T) {
	p := Paths{Data: t.TempDir()}
	since := time.Now()
	if Connected(p, "chrome-a", since) {
		t.Fatal("connected without a lock file")
	}
	unlock, err := TryLock(p.ProfileLock("chrome-a"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	later := since.Add(time.Second)
	if err := os.Chtimes(p.ProfileLock("chrome-a"), later, later); err != nil {
		t.Fatal(err)
	}
	if !Connected(p, "chrome-a", since) {
		t.Fatal("touched lock file not seen")
	}
}
