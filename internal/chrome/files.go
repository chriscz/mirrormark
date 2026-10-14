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

const (
	// ExternalDir: on Linux, branded Chrome installs a CRX named by a JSON
	// file here into every profile, without a prompt, at its next start.
	ExternalDir = "/usr/share/google-chrome/extensions"
	// SystemHostDir: native host manifests here reach every Chrome data
	// directory, not only the default one.
	SystemHostDir = "/etc/opt/chrome/native-messaging-hosts"
)

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
	e.CRX = filepath.Join(dataDir, "mirrormark.crx")
	return e, WriteCRX(dir, key, e.CRX)
}

type externalJSON struct {
	CRX     string `json:"external_crx"`
	Version string `json:"external_version"` // must equal the CRX's manifest version
}

type hostManifestJSON struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

func indented[T externalJSON | hostManifestJSON](v T) []byte {
	b, _ := json.MarshalIndent(v, "", "  ") // cannot fail for these types
	return append(b, '\n')
}

// SystemFilePaths are the external extension file for extension id and the
// native host manifest, in that order.
func SystemFilePaths(id string) []string {
	return []string{filepath.Join(ExternalDir, id+".json"), filepath.Join(SystemHostDir, extension.HostName+".json")}
}

// SystemFiles are the two files that tell Chrome about the extension and
// let it start bin as the native host, in SystemFilePaths order.
func SystemFiles(e Extension, bin string) []File {
	p := SystemFilePaths(e.ID)
	return []File{
		{p[0], indented(externalJSON{CRX: e.CRX, Version: e.Version})},
		{p[1], indented(hostManifestJSON{Name: extension.HostName, Description: "mirrormark bookmark sync",
			Path: bin, Type: "stdio", AllowedOrigins: []string{"chrome-extension://" + e.ID + "/"}})},
	}
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
