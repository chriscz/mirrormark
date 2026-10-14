package db

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrLocked means another process holds the lock.
var ErrLocked = errors.New("locked by another process")

// Lock takes an exclusive flock on path, waiting for it. The kernel releases
// it if the process dies, so a crashed host never leaves a stale lock.
func Lock(path string) (func(), error) { return lock(path, 0) }

// TryLock is Lock without waiting; it returns ErrLocked if the lock is held.
func TryLock(path string) (func(), error) { return lock(path, syscall.LOCK_NB) }

func lock(path string, flags int) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|flags); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}

// Connected reports whether a host has said hello for profile id since
// since. The host touches the profile's lock file on each hello; reading
// its mtime never takes the lock, so it cannot race the host for it.
func Connected(p Paths, id string, since time.Time) bool {
	info, err := os.Stat(p.ProfileLock(id))
	return err == nil && info.ModTime().After(since)
}
