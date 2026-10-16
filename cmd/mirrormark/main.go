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
`

func usage(w io.Writer) { fmt.Fprint(w, usageText) }

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
