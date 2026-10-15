package chrome

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// BindWait is how long install waits, after opening the bind page once,
// for the extension to report in. It covers a cold start of the browser
// and of the extension's worker; a slow first LibreWolf start in a
// container took over 10 seconds.
var BindWait = 30 * time.Second

// Open starts bin with args once, in its own session so that a browser it
// starts outlives mirrormark, and waits up to BindWait for reported to
// say the extension reported in since. It does not retry: install opens
// the page only where the profile's files show the extension installed,
// so the page cannot load before the extension.
func Open(bin string, args []string, reported func(since time.Time) bool) (bool, error) {
	start := time.Now()
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	go cmd.Wait()
	for time.Since(start) < BindWait {
		if reported(start) {
			return true, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false, nil
}

// Bind opens bind.html#<dbID>.<token> once in one of b's profiles with bin and
// waits for the extension to report in.
func (b Browser) Bind(bin, userDataDir, profileDir, extID, dbID, token string, reported func(since time.Time) bool) error {
	def, _ := b.DefaultUserDataDir()
	var args []string
	if userDataDir != def {
		args = append(args, "--user-data-dir="+userDataDir)
	}
	args = append(args, "--profile-directory="+profileDir, "chrome-extension://"+extID+"/bind.html#"+dbID+"."+token)
	ok, err := Open(bin, args, reported)
	if err != nil || ok {
		return err
	}
	return fmt.Errorf("mirrormark did not report in within %v of opening the page; look at the tab %s opened", BindWait, b.Name)
}
