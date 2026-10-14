package db

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const dbHeader = "# Written by mirrormark. You may edit the profile block; do not edit below it.\n"

// WriteAtomic writes data to a temporary file in path's directory, flushes
// it, and renames it over path, so a crash leaves the old file or the new one.
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

func marshal[T *DB | *Config](header string, v T) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(header)
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
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

func SaveDB(path string, d *DB) error {
	data, err := marshal(dbHeader, d)
	if err != nil {
		return err
	}
	return WriteAtomic(path, data, 0o600)
}

// LoadAll reads every database file. Files that fail to load are reported
// by id instead of being skipped silently.
func LoadAll(p Paths) (map[string]*DB, map[string]error) {
	dbs, errs := map[string]*DB{}, map[string]error{}
	names, _ := filepath.Glob(filepath.Join(p.ProfilesDir(), "*.yaml"))
	for _, name := range names {
		id := strings.TrimSuffix(filepath.Base(name), ".yaml")
		d, err := LoadDB(name)
		if err != nil {
			errs[id] = err
			continue
		}
		dbs[id] = d
	}
	return dbs, errs
}

// Backup copies the database file to backups/<id>/<date>.yaml before the
// first write of each day and keeps the newest 30. One backup a day means a
// burst of bad rounds cannot rotate every good copy away.
func Backup(p Paths, id string, now time.Time) error {
	data, err := os.ReadFile(p.ProfileFile(id))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	dir := p.BackupDir(id)
	dst := filepath.Join(dir, now.Format("2006-01-02")+".yaml")
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if err := WriteAtomic(dst, data, 0o600); err != nil {
		return err
	}
	names, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	slices.Sort(names)
	for len(names) > 30 {
		if err := os.Remove(names[0]); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}
