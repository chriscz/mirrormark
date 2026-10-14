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

// Retry starts bin with args up to bindTries times, each in its own session
// so that a browser it starts outlives mirrormark, and after each waits up
// to BindWait for bound to report a hello since that attempt started. It
// retries because a browser can open the page before the extension loads.
func Retry(bin string, args []string, bound func(since time.Time) bool) (bool, error) {
	for range bindTries {
		start := time.Now()
		cmd := exec.Command(bin, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return false, err
		}
		go cmd.Wait()
		for time.Since(start) < BindWait {
			if bound(start) {
				return true, nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return false, nil
}

// Bind opens bind.html#<dbID> in one of b's profiles with bin and waits
// for bound to report the host's hello since the attempt started.
func (b Browser) Bind(bin, userDataDir, profileDir, extID, dbID string, bound func(since time.Time) bool) error {
	def, _ := b.DefaultUserDataDir()
	var args []string
	if userDataDir != def {
		args = append(args, "--user-data-dir="+userDataDir)
	}
	args = append(args, "--profile-directory="+profileDir, "chrome-extension://"+extID+"/bind.html#"+dbID)
	ok, err := Retry(bin, args, bound)
	if err != nil || ok {
		return err
	}
	return fmt.Errorf("profile %s did not confirm within %d tries; check that %s was restarted after install and that mirrormark is enabled in chrome://extensions there, then run %s again", profileDir, bindTries, b.Name, b.InstallCommand())
}
