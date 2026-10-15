package firefox

import (
	"fmt"
	"time"

	"github.com/chriscz/mirrormark/internal/chrome"
)

// Bind opens bind.html#<dbID> once in the profile, under the add-on's
// UUID from ReadState, and waits for the add-on to report in. about:blank
// opens beside the page because the page closes its own tab, and closing
// the only tab of the only window quits Firefox.
func (b Browser) Bind(bin, profileDir, uuid, dbID string, reported func(since time.Time) bool) error {
	ok, err := chrome.Open(bin, []string{"--profile", profileDir, "about:blank", "moz-extension://" + uuid + "/bind.html#" + dbID}, reported)
	if err != nil || ok {
		return err
	}
	return fmt.Errorf("mirrormark did not report in within %v of opening the page; look at the tab %s opened", chrome.BindWait, b.Name)
}
