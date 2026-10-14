package firefox

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/chriscz/mirrormark/internal/chrome"
)

// UUIDWait is how long Bind waits for Firefox to install the add-on and
// record its UUID after starting it.
var UUIDWait = 15 * time.Second

// Bind opens bind.html#<dbID> in the profile and waits for bound to report
// the host's hello, with the same tries as for Chrome. It needs the
// add-on's UUID from prefs.js; if Firefox has not recorded one, Bind starts
// Firefox in the profile, which installs the add-on if it is not running
// there yet, and waits for the UUID. about:blank opens beside the page
// because the page closes its own tab, and closing the only tab of the
// only window quits Firefox.
func (b Browser) Bind(bin, profileDir, dbID string, bound func(since time.Time) bool) error {
	uuid, err := UUID(profileDir)
	if err != nil {
		return err
	}
	if uuid == "" {
		cmd := exec.Command(bin, "--profile", profileDir, "about:blank")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // it must outlive us
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		for start := time.Now(); uuid == "" && time.Since(start) < UUIDWait; time.Sleep(200 * time.Millisecond) {
			if uuid, err = UUID(profileDir); err != nil {
				return err
			}
		}
		if uuid == "" {
			return fmt.Errorf("%s has not installed mirrormark in %s; restart %s there (close every window), then run mirrormark install --browser %s again", b.Name, profileDir, b.Name, b.Config)
		}
	}
	ok, err := chrome.Retry(bin, []string{"--profile", profileDir, "about:blank", "moz-extension://" + uuid + "/bind.html#" + dbID}, bound)
	if err != nil || ok {
		return err
	}
	return fmt.Errorf("profile %s did not confirm; check that mirrormark is enabled in about:addons there, then run mirrormark install --browser %s again", profileDir, b.Config)
}
