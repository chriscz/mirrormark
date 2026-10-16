// Command dist writes mirrormark's release archives for make dist. Every
// entry is dated SOURCE_DATE_EPOCH and owned by root, in a fixed order, so
// two builds of one commit give the same bytes.
//
//	dist tar OUT.tar.gz DIR FILE...  FILEs under DIR/, gzipped
//	dist zip OUT.zip                 the extension's files, for Load unpacked
//	dist xpi OUT.xpi                 the XPI install writes into Firefox
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/chriscz/mirrormark/extension"
	"github.com/chriscz/mirrormark/internal/firefox"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dist:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	usage := errors.New("usage: dist tar OUT DIR FILE... | dist zip OUT | dist xpi OUT")
	if len(args) < 2 {
		return usage
	}
	var data []byte
	var err error
	switch args[0] {
	case "tar":
		if len(args) < 4 {
			return usage
		}
		data, err = withSourceDate(func(t time.Time) ([]byte, error) { return tarGz(t, args[2], args[3:]) })
	case "zip":
		data, err = withSourceDate(extension.Zip)
	case "xpi":
		// Byte for byte the XPI install writes, so dated as install dates it.
		data, err = firefox.XPI()
	default:
		return usage
	}
	if err != nil {
		return err
	}
	return os.WriteFile(args[1], data, 0o644)
}

// withSourceDate runs build with SOURCE_DATE_EPOCH, which make dist sets.
func withSourceDate(build func(time.Time) ([]byte, error)) ([]byte, error) {
	s := os.Getenv("SOURCE_DATE_EPOCH")
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("SOURCE_DATE_EPOCH %q: want seconds since 1970", s)
	}
	return build(time.Unix(sec, 0).UTC())
}

// tarGz is a gzipped tar of dir/ and each file under it, in the order
// given. Executable files keep mode 0755; the rest get 0644.
func tarGz(modified time.Time, dir string, files []string) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	tw := tar.NewWriter(zw)
	hdr := func(name string, mode int64, flag byte, size int64) *tar.Header {
		return &tar.Header{Typeflag: flag, Name: name, Mode: mode, Size: size, ModTime: modified, Format: tar.FormatUSTAR}
	}
	if err := tw.WriteHeader(hdr(dir+"/", 0o755, tar.TypeDir, 0)); err != nil {
		return nil, err
	}
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		mode := int64(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := tw.WriteHeader(hdr(dir+"/"+filepath.Base(f), mode, tar.TypeReg, int64(len(data)))); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	err = zw.Close()
	return buf.Bytes(), err
}
