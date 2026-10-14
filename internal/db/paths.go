// Package db reads and writes mirrormark's files: the user's config.yaml,
// one database file per browser profile, backups and locks.
package db

import (
	"os"
	"path/filepath"
)

// Paths holds mirrormark's three directories.
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

func (p Paths) ConfigFile() string           { return filepath.Join(p.Config, "config.yaml") }
func (p Paths) ProfilesDir() string          { return filepath.Join(p.Data, "profiles") }
func (p Paths) ProfileFile(id string) string { return filepath.Join(p.ProfilesDir(), id+".yaml") }
func (p Paths) ProfileLock(id string) string { return filepath.Join(p.ProfilesDir(), id+".lock") }
func (p Paths) GlobalLock() string           { return filepath.Join(p.Data, "lock") }
func (p Paths) BackupDir(id string) string   { return filepath.Join(p.Data, "backups", id) }
func (p Paths) LogFile() string              { return filepath.Join(p.State, "log") }
