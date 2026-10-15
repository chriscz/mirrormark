package extension

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/chriscz/mirrormark/internal/db"
	"github.com/chriscz/mirrormark/internal/host"
)

func TestUnpackWritesFilesAndKey(t *testing.T) {
	dir := t.TempDir()
	pub := []byte{1, 2, 3}
	if err := Unpack(dir, pub); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"manifest.json", "background.js", "bind.html", "bind.js", "popup.html", "popup.js", "ports.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Key                    string   `json:"key"`
		Permissions            []string `json:"permissions"`
		HostPermissions        []string `json:"host_permissions"`
		WebAccessibleResources []string `json:"web_accessible_resources"`
		Version                string   `json:"version"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.Key != base64.StdEncoding.EncodeToString(pub) {
		t.Fatalf("key %q", m.Key)
	}
	if strings.Join(m.Permissions, ",") != "alarms,bookmarks,nativeMessaging,storage" {
		t.Fatalf("permissions %v", m.Permissions)
	}
	if m.HostPermissions != nil || m.WebAccessibleResources != nil {
		t.Fatalf("host permissions %v, web-accessible resources %v", m.HostPermissions, m.WebAccessibleResources)
	}
	if v, _ := Version(); v != m.Version || v == "" {
		t.Fatalf("Version() %q, manifest %q", v, m.Version)
	}
}

func TestBackgroundUsesHostName(t *testing.T) {
	js, _ := Files.ReadFile("background.js")
	if !strings.Contains(string(js), `const HOST = "`+HostName+`";`) {
		t.Fatal("background.js and HostName disagree")
	}
}

func TestBackgroundSpeaksHostVersion(t *testing.T) {
	js, _ := Files.ReadFile("background.js")
	if !strings.Contains(string(js), "const V = "+strconv.Itoa(host.Version)+";") {
		t.Fatal("background.js and host.Version disagree")
	}
}

// A non-empty folder must survive a remove: Chrome refuses bookmarks.remove
// on one, while removeTree would take what the host means to keep. Any
// mention of it fails, so neither bookmarks["removeTree"] nor an alias
// slips past.
func TestBackgroundNeverRemovesTrees(t *testing.T) {
	js, _ := Files.ReadFile("background.js")
	if strings.Contains(strings.ToLower(string(js)), "removetree") {
		t.Fatal("background.js mentions removeTree")
	}
}

func TestBindAcceptsDatabaseIDs(t *testing.T) {
	js, _ := Files.ReadFile("bind.js")
	if !strings.Contains(string(js), db.ValidID.String()) {
		t.Fatalf("bind.js does not check ids with %s", db.ValidID)
	}
}

func TestPorts(t *testing.T) {
	got, err := Ports()
	if err != nil || !slices.Equal(got, []int{61959, 63018, 63454}) {
		t.Fatalf("Ports() = %v, %v", got, err)
	}
}
