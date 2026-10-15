package chrome

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// State is what a profile's own files say about mirrormark there. install
// reads it before it opens anything in the profile: a bind page opened
// where the extension is not loaded only shows the browser's "blocked"
// error page.
type State int

const (
	NeverRun        State = iota // the browser has never opened the profile
	NotInstalled                 // nothing mirrormark wrote reaches the profile
	NotInstalledYet              // mirrormark's files are in place; the browser has not installed it here
	Removed                      // the user removed it, and the browser will not install it again
	TurnedOff                    // installed, but the user turned it off
	Refused                      // installed, but the browser refuses to run it
	OldBuild                     // installed, but not this build
	Ready                        // installed, enabled, this build
)

// ProfileState is a profile's State, with the build found for OldBuild.
type ProfileState struct {
	State   State
	Version string
}

func (s ProfileState) String() string {
	switch s.State {
	case NeverRun:
		return "never run"
	case NotInstalled:
		return "not installed"
	case NotInstalledYet:
		return "not installed yet"
	case Removed:
		return "removed"
	case TurnedOff:
		return "turned off"
	case Refused:
		return "refused"
	case OldBuild:
		return "old build " + s.Version
	}
	return "ready"
}

// ReadState reads extension id's state from a Chromium profile's
// Preferences. filePlaced says whether this browser's external extension
// file is in place, which tells "not installed" from "not installed yet".
// Chromium writes Preferences in batches, up to about 20 seconds after a
// change, so a running browser's file can be behind; a clean close writes
// it at once. The file is not a documented interface.
func ReadState(profileDir, id, version string, filePlaced bool) (ProfileState, error) {
	path := filepath.Join(profileDir, "Preferences")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ProfileState{State: NeverRun}, nil
	}
	if err != nil {
		return ProfileState{}, err
	}
	var prefs struct {
		Extensions struct {
			Settings map[string]struct {
				Path string `json:"path"`
				// A list of reasons since about Chrome 120, a bit mask
				// before; either way, anything but none means off.
				DisableReasons json.RawMessage `json:"disable_reasons"`
				Manifest       struct {
					Version string `json:"version"`
				} `json:"manifest"`
			} `json:"settings"`
			ExternalUninstalls []string `json:"external_uninstalls"`
		} `json:"extensions"`
	}
	if err := json.Unmarshal(data, &prefs); err != nil {
		return ProfileState{}, fmt.Errorf("%s: %w", path, err)
	}
	e, ok := prefs.Extensions.Settings[id]
	switch r := bytes.TrimSpace(e.DisableReasons); {
	// A Remove leaves an empty entry and the id in external_uninstalls; a
	// copy loaded unpacked afterwards has a path again, and may not clear
	// that list.
	case (!ok || e.Path == "") && slices.Contains(prefs.Extensions.ExternalUninstalls, id):
		return ProfileState{State: Removed}, nil
	case !ok || e.Path == "":
		if filePlaced {
			return ProfileState{State: NotInstalledYet}, nil
		}
		return ProfileState{State: NotInstalled}, nil
	case len(r) > 0 && !bytes.Equal(r, []byte("[]")) && !bytes.Equal(r, []byte("0")):
		return ProfileState{State: TurnedOff}, nil
	case e.Manifest.Version != version:
		return ProfileState{State: OldBuild, Version: e.Manifest.Version}, nil
	}
	return ProfileState{State: Ready}, nil
}
