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

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/host"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usageText = `Usage: mirrormark <command> [options]

Commands:
  host        Run as the native messaging host (Chrome does this)
  help        Show this help
`

func usage(w io.Writer) { fmt.Fprint(w, usageText) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	// Chrome runs the host with the extension's origin as its only argument.
	if strings.HasPrefix(args[0], "chrome-extension://") || args[0] == "host" {
		return cmdHost(stdin, stdout, stderr)
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(stdout)
		return 0
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
	f, err := os.OpenFile(p.LogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(stderr, "mirrormark:", err)
		return 1
	}
	defer f.Close()
	log := slog.New(slog.NewTextHandler(f, nil)).With("pid", os.Getpid())
	if err := host.Run(context.Background(), stdin, stdout, host.Options{Paths: p, Log: log}); err != nil {
		log.Warn("host stopped", "err", err)
		return 1
	}
	return 0
}
