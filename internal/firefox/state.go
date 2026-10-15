package firefox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/chrome"
)

// ReadState reads the add-on's state in a Gecko profile from the XPI,
// extensions.json and the UUID in prefs.js, and returns the UUID with it.
// Firefox writes both files within about a second of a change. A user's
// Remove deletes the XPI and leaves no record, so it reads as not
// installed.
func ReadState(profileDir, version string) (chrome.ProfileState, string, error) {
	if _, err := os.Stat(XPIPath(profileDir)); errors.Is(err, fs.ErrNotExist) {
		return chrome.ProfileState{State: chrome.NotInstalled}, "", nil
	} else if err != nil {
		return chrome.ProfileState{}, "", err
	}
	uuid, err := UUID(profileDir)
	if err != nil {
		return chrome.ProfileState{}, "", err
	}
	path := filepath.Join(profileDir, "extensions.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return chrome.ProfileState{State: chrome.NotInstalledYet}, uuid, nil
	}
	if err != nil {
		return chrome.ProfileState{}, "", err
	}
	type addon struct {
		ID           string `json:"id"`
		Active       bool   `json:"active"`
		UserDisabled bool   `json:"userDisabled"`
		AppDisabled  bool   `json:"appDisabled"`
		Version      string `json:"version"`
	}
	var f struct {
		Addons []addon `json:"addons"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return chrome.ProfileState{}, "", fmt.Errorf("%s: %w", path, err)
	}
	i := slices.IndexFunc(f.Addons, func(a addon) bool { return a.ID == extension.GeckoID })
	if i < 0 {
		return chrome.ProfileState{State: chrome.NotInstalledYet}, uuid, nil
	}
	switch a := f.Addons[i]; {
	case a.UserDisabled:
		return chrome.ProfileState{State: chrome.TurnedOff}, uuid, nil
	case a.AppDisabled:
		return chrome.ProfileState{State: chrome.Refused}, uuid, nil
	case a.Version != version:
		return chrome.ProfileState{State: chrome.OldBuild, Version: a.Version}, uuid, nil
	case !a.Active || uuid == "":
		return chrome.ProfileState{State: chrome.NotInstalledYet}, uuid, nil
	}
	return chrome.ProfileState{State: chrome.Ready}, uuid, nil
}
