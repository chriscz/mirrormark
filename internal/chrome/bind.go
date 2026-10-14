package chrome

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// BindWait is how long Bind waits for the profile to confirm each attempt.
var BindWait = 4 * time.Second

// bindTries is how often Bind opens the page before it gives up.
const bindTries = 3

// Bind opens bind.html#<dbID> in one profile and waits for bound to report
// the host's hello since the attempt started. It retries because on a
// profile's first start after the extension was installed, the page can
// load before the extension and show an error.
func Bind(chromeBin, userDataDir, profileDir, extID, dbID string, bound func(since time.Time) bool) error {
	def, _ := DefaultUserDataDir()
	var args []string
	if userDataDir != def {
		args = append(args, "--user-data-dir="+userDataDir)
	}
	args = append(args, "--profile-directory="+profileDir, "chrome-extension://"+extID+"/bind.html#"+dbID)
	for range bindTries {
		start := time.Now()
		cmd := exec.Command(chromeBin, args...)
		// If Chrome is not running, this starts it; it must outlive us.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		for time.Since(start) < BindWait {
			if bound(start) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return fmt.Errorf("profile %s did not confirm within %d tries; check that Chrome was restarted after install and that mirrormark is enabled in chrome://extensions there, then run mirrormark install again", profileDir, bindTries)
}
