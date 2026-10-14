package db

import (
	"path/filepath"
	"testing"
)

func TestDefaultPathsFollowXDG(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "/x/data")
	t.Setenv("XDG_STATE_HOME", "relative/ignored")
	p, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	want := Paths{
		Config: "/home/u/.config/mirrormark",
		Data:   "/x/data/mirrormark",
		State:  "/home/u/.local/state/mirrormark",
	}
	if p != want {
		t.Fatalf("got %+v, want %+v", p, want)
	}
	if got := p.ProfileFile("chrome-a"); got != filepath.Join("/x/data/mirrormark/profiles", "chrome-a.yaml") {
		t.Fatalf("ProfileFile = %q", got)
	}
}
