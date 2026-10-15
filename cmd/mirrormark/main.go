// Command mirrormark keeps bookmark folders the same across browser profiles
// through YAML files on this computer.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

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
  host        Run as the native messaging host (the browser does this)
  help        Show this help
`

func usage(w io.Writer) { fmt.Fprint(w, usageText) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	// Chrome runs the host with the extension's origin as its only argument;
	// Firefox with the host manifest's path and the extension's id.
	if strings.HasPrefix(args[0], "chrome-extension://") ||
		strings.HasSuffix(args[0], "/"+extension.HostName+".json") || args[0] == "host" {
		return cmdHost(stdin, stdout, stderr)
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
	}
	fmt.Fprintf(stderr, "mirrormark: unknown command %q\n", args[0])
	usage(stderr)
	return 2
}

// cmdHost logs to the log file: stdout belongs to the protocol.
func cmdHost(stdin io.Reader, stdout, stderr io.Writer) int {
	p, err := db.DefaultPaths()
	if err != nil {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	if err := os.MkdirAll(p.State, 0o700); err != nil {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	// Every profile's host appends to this one log; keep it bounded. Two
	// hosts starting at once may both rotate, and so lose the old log.
	rotateErr := rotate(p.LogFile())
	f, err := os.OpenFile(p.LogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	defer f.Close()
	log := slog.New(slog.NewTextHandler(f, nil)).With("pid", os.Getpid())
	if rotateErr != nil {
		log.Warn("cannot rotate the log", "err", rotateErr)
	}
	if err := host.Run(context.Background(), stdin, stdout, host.Options{Paths: p, Log: log}); err != nil {
		log.Warn("host stopped", "err", err)
		return 1
	}
	return 0
}

// maxLog is the size above which a starting host moves the log to log.1,
// replacing the previous one.
const maxLog = 1 << 20

func rotate(path string) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxLog {
		return nil
	}
	return os.Rename(path, path+".1")
}
