// Command mirrormark keeps bookmark folders the same across browser profiles
// through YAML files on this computer.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/host"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usageText = `Usage: mirrormark <command> [options]

Commands:
  install     List a browser's profiles, or install into Google Chrome,
              Brave, Firefox Developer Edition or LibreWolf and bind the
              profiles named
              [--browser chrome|brave|firefox|librewolf] [--user-data-dir DIR]
              [--profile ID_OR_NUMBER]...
  uninstall   Remove from a browser; keeps your files unless --purge
              [--browser chrome|brave|firefox|librewolf] [--purge]
  unpack      Write the extension, with this install's key, for Load unpacked
              [DIR]
  serve       Run the server the extensions connect to
  help        Show this help

Sharing folders:
  List the folders to share in config.yaml. Each union names, for each
  profile id, the folder that profile shares, for example:

%s
  A profile's id is in the DATABASE column of mirrormark install --browser B,
  or is the <id> in the line bound: <profile> -> <id> that install prints. A
  path starts with toolbar, other or mobile, or menu in Firefox Developer
  Edition and LibreWolf, then the folder names as the bookmark manager shows
  them; write \/ for a slash in a name. One profile's folders cannot be
  inside each other. mirrormark picks up a change within seconds, with no
  restart, and the log records each one.

Files:
  config.yaml      %s
  databases        %s/<id>.yaml
  backups          %s/<id>/
  key, extension   %s/ (extension.pem, mirrormark.crx, extension/)
  log              %s
  serve.lock       %s
  service          %s
`

// usage prints the help, with each file where it is for this user.
func usage(w io.Writer) {
	p, err := db.DefaultPaths()
	if err != nil { // no home directory: name the defaults
		p = db.Paths{Config: "~/.config/mirrormark", Data: "~/.local/share/mirrormark", State: "~/.local/state/mirrormark"}
	}
	fmt.Fprintf(w, usageText, unionExample("    ", "chrome-personal", "firefox-work"), p.ConfigFile(), p.ProfilesDir(),
		filepath.Join(p.Data, "backups"), p.Data, p.LogFile(), p.ServeLock(), unitPath(p))
}

// unionExample is a config.yaml that shares toolbar/Work of the profiles
// with these ids, each line indented by indent. With one id, a comment
// stands for the second.
func unionExample(indent string, ids ...string) string {
	lines := []string{"format: 1", "unions:", "  work:"}
	for _, id := range ids {
		lines = append(lines, "    "+id+": toolbar/Work")
	}
	if len(ids) == 1 {
		lines = append(lines, "    # another profile's id: toolbar/Work")
	}
	return indent + strings.Join(lines, "\n"+indent) + "\n"
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	case "install":
		return cmdInstall(args[1:], stdout, stderr)
	case "uninstall":
		return cmdUninstall(args[1:], stdout, stderr)
	case "unpack":
		return cmdUnpack(args[1:], stdout, stderr)
	case "serve":
		return cmdServe(args[1:], stderr)
	}
	fmt.Fprintf(stderr, "mirrormark: unknown command %q\n", args[0])
	usage(stderr)
	return 2
}

// openLog opens the log the server appends to, rotating it first if it is
// large. The caller closes the file.
func openLog(p db.Paths) (*slog.Logger, *os.File, error) {
	if err := os.MkdirAll(p.State, 0o700); err != nil {
		return nil, nil, err
	}
	rotateErr := rotate(p.LogFile())
	f, err := os.OpenFile(p.LogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	log := slog.New(slog.NewTextHandler(f, nil)).With("pid", os.Getpid())
	if rotateErr != nil {
		log.Warn("cannot rotate the log", "err", rotateErr)
	}
	return log, f, nil
}

// cmdServe runs the server until it is told to stop. One runs per user:
// it holds the serve lock while it runs.
func cmdServe(args []string, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "mirrormark: usage: mirrormark serve")
		return 2
	}
	if refuseRoot("serve", "", stderr) {
		return 1
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	p, err := db.DefaultPaths()
	if err != nil {
		return fail(err)
	}
	unlock, err := db.TryLock(p.ServeLock())
	if errors.Is(err, db.ErrLocked) {
		return fail(errors.New("mirrormark is already running for this user"))
	}
	if err != nil {
		return fail(err)
	}
	defer unlock()
	log, f, err := openLog(p)
	if err != nil {
		return fail(err)
	}
	defer f.Close()
	ports, err := extension.Ports()
	if err != nil {
		return fail(err)
	}
	ln, err := host.Listen(ports)
	if err != nil {
		log.Warn("cannot listen", "err", err)
		return fail(err)
	}
	log.Info("listening", "addr", ln.Addr().String())
	fmt.Fprintf(stderr, "mirrormark: listening on %s; the log is %s\n", ln.Addr(), p.LogFile())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := host.Serve(ctx, ln, host.Options{Paths: p, Log: log}); err != nil {
		log.Warn("server stopped", "err", err)
		return fail(err)
	}
	return 0
}

// maxLog is the size above which a starting server moves the log to log.1,
// replacing the previous one.
const maxLog = 1 << 20

func rotate(path string) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxLog {
		return nil
	}
	return os.Rename(path, path+".1")
}
