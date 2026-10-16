package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The same files and date give the same bytes, with each file under the
// directory, owned by root, dated, and executable only if it was.
func TestTarGzIsReproducible(t *testing.T) {
	d := t.TempDir()
	bin, doc := filepath.Join(d, "mirrormark"), filepath.Join(d, "README.md")
	if err := os.WriteFile(bin, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, []byte("readme"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Unix(1700000000, 0).UTC()
	a, err := tarGz(when, "mm_1", []string{bin, doc})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(bin, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	b, err := tarGz(when, "mm_1", []string{bin, doc})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("two builds differ")
	}
	zr, err := gzip.NewReader(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var got []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !h.ModTime.Equal(when) || h.Uid != 0 || h.Gid != 0 {
			t.Errorf("%s: time %v uid %d gid %d", h.Name, h.ModTime, h.Uid, h.Gid)
		}
		got = append(got, h.Name+" "+os.FileMode(h.Mode).String())
	}
	want := []string{"mm_1/ -rwxr-xr-x", "mm_1/mirrormark -rwxr-xr-x", "mm_1/README.md -rw-r--r--"}
	if !slices.Equal(got, want) {
		t.Fatalf("entries %q, want %q", got, want)
	}
}

func TestSourceDateEpochIsRequired(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "")
	if err := run([]string{"zip", filepath.Join(t.TempDir(), "x.zip")}); err == nil {
		t.Fatal("no error without SOURCE_DATE_EPOCH")
	}
}
