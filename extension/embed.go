// Package extension holds the browser extension's files, embedded in the
// binary so one file installs everything. The extension is plain JavaScript
// with no build step: these files are what Chrome and Firefox load.
//
// The extension stays dormant until bind.html#<database id>.<token> stores
// the profile's binding in chrome.storage.local. It then connects to
// mirrormark serve over a WebSocket on 127.0.0.1, trying the ports in
// ports.json in order, and says hello. The server proves it holds the
// profile's token before the extension proves it does; the token itself
// never goes on the socket. The extension sends the whole bookmark tree
// after the proof, after each batch it applies, and after 0.5 s without
// bookmark events. At the first
// bookmark event of a burst it sends "changed", so the host waits for that
// tree. It runs each apply batch's ops in order, stops at the first failure
// and answers the rest "not run". The popup shows the binding, why the host
// stopped if it did, and the problems the last round found.
//
// manifest.json holds the version; Version reads it from there.
package extension

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
)

// GeckoID is the extension's id in Firefox. Firefox does not derive ids from
// a key, so every install shares it.
const GeckoID = "mirrormark@chriscz.github.io"

//go:embed manifest.json background.js bind.html bind.js popup.html popup.js ports.json
var Files embed.FS

// Unpack writes the extension to dir and adds the public key to the
// manifest as "key", so Chrome derives the install's extension id from it.
func Unpack(dir string, publicKeyDER []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(Files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := Files.ReadFile(name)
		if err != nil {
			return err
		}
		if name == "manifest.json" {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(data, &m); err != nil {
				return err
			}
			if m["key"], err = json.Marshal(base64.StdEncoding.EncodeToString(publicKeyDER)); err != nil {
				return err
			}
			if data, err = json.MarshalIndent(m, "", "  "); err != nil {
				return err
			}
		}
		return os.WriteFile(filepath.Join(dir, name), data, 0o644)
	})
}

// Version is the extension's version from manifest.json, the one place it
// is written. Chrome installs a new CRX only when this changes.
func Version() (string, error) {
	data, err := Files.ReadFile("manifest.json")
	if err != nil {
		return "", err
	}
	var m struct {
		Version string `json:"version"`
	}
	err = json.Unmarshal(data, &m)
	return m.Version, err
}

// Ports are the ports the server may listen on, in the order it tries
// them, from ports.json, which the extension reads too.
func Ports() ([]int, error) {
	data, err := Files.ReadFile("ports.json")
	if err != nil {
		return nil, err
	}
	var ports []int
	err = json.Unmarshal(data, &ports)
	return ports, err
}
