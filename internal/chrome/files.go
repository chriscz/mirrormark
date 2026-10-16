package chrome

import (
	"bytes"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/db"
)

// LegacyHostManifest is where builds before the server put the native
// host manifest every Chromium browser read. uninstall removes it when it
// names this install's extension.
const LegacyHostManifest = "/etc/opt/chrome/native-messaging-hosts/com.github.chriscz.mirrormark.json"

// Extension is the unpacked extension and its CRX, under the install's key.
type Extension struct{ ID, Version, Dir, CRX string }

// File is a file to write whole.
type File struct {
	Path string
	Data []byte
}

// Unpack writes the embedded extension to dir with the install's key from
// dataDir, created on first use, so a copy loaded unpacked has the same id
// as the installed one. It overwrites the extension's files and leaves
// anything else in dir alone.
func Unpack(dataDir, dir string) (Extension, error) {
	e, _, err := unpack(dataDir, dir)
	return e, err
}

func unpack(dataDir, dir string) (Extension, *rsa.PrivateKey, error) {
	key, err := LoadOrCreateKey(filepath.Join(dataDir, "extension.pem"))
	if err != nil {
		return Extension{}, nil, err
	}
	pub := PublicKeyDER(key)
	e := Extension{ID: ExtensionID(pub), Dir: dir}
	if e.Version, err = extension.Version(); err != nil {
		return e, nil, err
	}
	return e, key, extension.Unpack(dir, pub)
}

// Prepare unpacks the embedded extension into dataDir/extension, replacing
// whatever was there, and packs it as dataDir/mirrormark.crx.
func Prepare(dataDir string) (Extension, error) {
	dir := filepath.Join(dataDir, "extension")
	// mirrormark's own folder: clear it so a file a newer build dropped
	// does not end up in the CRX.
	if err := os.RemoveAll(dir); err != nil {
		return Extension{}, err
	}
	e, key, err := unpack(dataDir, dir)
	if err != nil {
		return e, err
	}
	e.CRX = CRXPath(dataDir)
	return e, WriteCRX(dir, key, e.CRX)
}

// CRXPath is where Prepare packs the CRX in dataDir.
func CRXPath(dataDir string) string { return filepath.Join(dataDir, "mirrormark.crx") }

type externalJSON struct {
	CRX     string `json:"external_crx"`
	Version string `json:"external_version"` // must equal the CRX's manifest version
}

func indented(v externalJSON) []byte {
	b, _ := json.MarshalIndent(v, "", "  ") // cannot fail for this type
	return append(b, '\n')
}

// ExternalFile is the file that tells b to install the extension id.
func (b Browser) ExternalFile(id string) string { return filepath.Join(b.ExternalDir, id+".json") }

// ExternalCRX is the CRX path the external extension file at path names,
// or "" if it is not one of ours (another extension's, say).
func ExternalCRX(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var e externalJSON
	if json.Unmarshal(data, &e) != nil {
		return "", nil
	}
	return e.CRX, nil
}

// SystemFiles is the file that tells b to install the extension: b's
// external file.
func (b Browser) SystemFiles(e Extension) []File {
	return []File{{b.ExternalFile(e.ID), indented(externalJSON{CRX: e.CRX, Version: e.Version})}}
}

// FilesCurrent reports whether every file exists with exactly its data.
func FilesCurrent(files []File) bool {
	for _, f := range files {
		if data, err := os.ReadFile(f.Path); err != nil || !bytes.Equal(data, f.Data) {
			return false
		}
	}
	return true
}

// WriteFiles writes world-readable files and directories: the browser
// reads them as the user. A file that exists keeps its mode, and one that
// is a symlink is written where the link points, so a user's own file
// (user.js kept with their dotfiles, say) stays as they set it up.
func WriteFiles(files []File) error {
	for _, f := range files {
		path, mode := f.Path, fs.FileMode(0o644)
		real, err := filepath.EvalSymlinks(path)
		switch {
		case err == nil:
			path = real
			fi, err := os.Stat(path)
			if err != nil {
				return err
			}
			mode = fi.Mode().Perm()
		case !errors.Is(err, fs.ErrNotExist):
			return err
		default:
			// A link to nothing would be replaced by a file.
			if _, err := os.Lstat(path); err == nil {
				return fmt.Errorf("%s is a symlink to a file that does not exist", path)
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := db.WriteAtomic(path, f.Data, mode); err != nil {
			return err
		}
	}
	return nil
}

// RemoveFiles removes each path; one that is already gone is fine.
func RemoveFiles(paths []string) error {
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
