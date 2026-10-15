// Package db reads and writes mirrormark's files: the user's config.yaml,
// one database file per browser profile, backups and locks.
package db

import (
	"os"
	"path/filepath"
)

// Paths holds mirrormark's three directories. Its methods build the paths of
// the files inside them; they do not touch the file system.
type Paths struct {
	Config string // config.yaml
	Data   string // profiles/, backups/, lock, extension/
	State  string // the host's log
}

// DefaultPaths follows the XDG base directory spec: a variable counts only
// if it holds an absolute path.
func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	dir := func(env, def string) string {
		if v := os.Getenv(env); filepath.IsAbs(v) {
			return filepath.Join(v, "mirrormark")
		}
		return filepath.Join(home, def, "mirrormark")
	}
	return Paths{
		Config: dir("XDG_CONFIG_HOME", ".config"),
		Data:   dir("XDG_DATA_HOME", ".local/share"),
		State:  dir("XDG_STATE_HOME", ".local/state"),
	}, nil
}

// ConfigFile is the user's config.yaml.
func (p Paths) ConfigFile() string { return filepath.Join(p.Config, "config.yaml") }

// ProfilesDir holds one database file and one lock file per profile.
func (p Paths) ProfilesDir() string { return filepath.Join(p.Data, "profiles") }

// ProfileFile is profile id's database file.
func (p Paths) ProfileFile(id string) string { return filepath.Join(p.ProfilesDir(), id+".yaml") }

// ProfileLock is profile id's lock file; its mtime is the last hello.
func (p Paths) ProfileLock(id string) string { return filepath.Join(p.ProfilesDir(), id+".lock") }

// ServeLock is held by the running server, so one runs per user.
func (p Paths) ServeLock() string { return filepath.Join(p.Data, "serve.lock") }

// GlobalLock serialises rounds across all profiles.
func (p Paths) GlobalLock() string { return filepath.Join(p.Data, "lock") }

// BackupDir holds profile id's daily backups.
func (p Paths) BackupDir(id string) string { return filepath.Join(p.Data, "backups", id) }

// LogFile is the host's log.
func (p Paths) LogFile() string { return filepath.Join(p.State, "log") }
