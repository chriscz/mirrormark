package db

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const dbHeader = "# Written by mirrormark. You may edit the profile block; do not edit below it.\n"

// WriteAtomic writes data to a temporary file in path's directory, flushes
// it, and renames it over path, so a crash leaves the old file or the new one.
// On any error before the rename, path is unchanged and the temporary file is
// removed. An error from syncing the directory after the rename means the new
// file is in place but may not survive a crash.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func marshal(header string, d *DB) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// unmarshalStrict rejects unknown keys, so a typo in a hand edit is an error.
func unmarshalStrict[T *DB | *Config](data []byte, v T) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(v)
}

// LoadDB reads one database file. It rejects a file that is not valid YAML,
// has a key the DB type does not know, has a format other than 1, or holds
// an invalid profile id.
func LoadDB(path string) (*DB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var d DB
	if err := unmarshalStrict(data, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if d.Format != 1 {
		return nil, fmt.Errorf("%s: unsupported format %d", path, d.Format)
	}
	if !ValidID.MatchString(d.Profile.ID) {
		return nil, fmt.Errorf("%s: invalid profile id %q", path, d.Profile.ID)
	}
	return &d, nil
}

// SaveDB writes d to path with the "written by mirrormark" header, atomically
// and readable only by the user (mode 0600).
func SaveDB(path string, d *DB) error {
	data, err := marshal(dbHeader, d)
	if err != nil {
		return err
	}
	return WriteAtomic(path, data, 0o600)
}

// LoadAll reads every *.yaml file in the profiles directory, keyed by the
// file name without ".yaml". Files that fail to load are reported in the
// error map instead of being skipped silently, and so is a file whose name
// is not a valid id or does not match the profile id inside it (for example
// a copy of another profile's file). A missing profiles directory means no
// profiles; any other error listing it is reported under the directory's
// path, which is never a valid id.
func LoadAll(p Paths) (map[string]*DB, map[string]error) {
	dbs, errs := map[string]*DB{}, map[string]error{}
	dir := p.ProfilesDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return dbs, errs
	}
	if err != nil {
		errs[dir] = err
		return dbs, errs
	}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if !ValidID.MatchString(id) {
			errs[id] = fmt.Errorf("%s: file name is an invalid profile id", path)
			continue
		}
		d, err := LoadDB(path)
		if err != nil {
			errs[id] = err
			continue
		}
		if d.Profile.ID != id {
			errs[id] = fmt.Errorf("%s: holds profile %q, not %q; was it copied from another profile?", path, d.Profile.ID, id)
			continue
		}
		dbs[id] = d
	}
	return dbs, errs
}

// backupDate is the layout of a backup file's name, without ".yaml".
const backupDate = "2006-01-02"

// keepBackups is how many daily backups Backup keeps per profile.
const keepBackups = 30

// Backup copies the database file to backups/<id>/<date>.yaml before the
// first write of each day and keeps the newest 30. One backup a day means a
// burst of bad rounds cannot rotate every good copy away. Rotation deletes
// only names of the form YYYY-MM-DD.yaml; anything else in the directory is
// left alone. The date comes from now as given, so callers must pass times
// in one consistent time zone (the host uses local time).
func Backup(p Paths, id string, now time.Time) error {
	data, err := os.ReadFile(p.ProfileFile(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	dir := p.BackupDir(id)
	dst := filepath.Join(dir, now.Format(backupDate)+".yaml")
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := WriteAtomic(dst, data, 0o600); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string // sorted, as ReadDir returns them, so oldest first
	for _, e := range entries {
		date, ok := strings.CutSuffix(e.Name(), ".yaml")
		if _, err := time.Parse(backupDate, date); ok && err == nil && len(date) == len(backupDate) {
			names = append(names, e.Name())
		}
	}
	for len(names) > keepBackups {
		if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}
