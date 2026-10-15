package db

import (
	"regexp"
	"slices"
)

// ValidID matches database ids. They are file names and appear in bind URLs.
var ValidID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Node is a bookmark (URL set) or a folder (URL empty) inside a member folder.
type Node struct {
	Title    string  `yaml:"title"`
	URL      string  `yaml:"url,omitempty"`
	ID       string  `yaml:"id,omitempty"`     // the browser's node id in this profile
	M        string  `yaml:"m,omitempty"`      // mirrormark's id, the same in every profile
	By       string  `yaml:"by,omitempty"`     // label of the profile that last changed it
	Suffix   string  `yaml:"suffix,omitempty"` // label mirrormark appended to Title as " (label)"
	Children []*Node `yaml:"children,omitempty"`
}

// Member is one profile's record of one union: its member folder's contents
// as of the profile's last completed round.
type Member struct {
	Seq      int64   `yaml:"seq"`  // orders this union's records across members
	Path     string  `yaml:"path"` // the config path the record was made for
	ID       string  `yaml:"id"`   // browser id of the member folder
	Children []*Node `yaml:"children,omitempty"`
}

// Profile identifies the browser profile a database file belongs to. ID
// matches the file name; Label is what suffixes and "by" fields show.
type Profile struct {
	ID      string `yaml:"id"`
	Label   string `yaml:"label"`
	Browser string `yaml:"browser"`
	Path    string `yaml:"path"`
	Origin  string `yaml:"origin,omitempty"` // the extension's origin in this profile; written by install
	Token   string `yaml:"token,omitempty"`  // the secret the extension proves its binding with; written by install
}

// DB is one profile's database file.
type DB struct {
	Format  int                `yaml:"format"`
	Profile Profile            `yaml:"profile"`
	Unions  map[string]*Member `yaml:"unions,omitempty"`
}

// Config is config.yaml: union name → profile id → folder path.
type Config struct {
	Format int                          `yaml:"format"`
	Unions map[string]map[string]string `yaml:"unions"`
}

// EqualNodes compares two trees field by field, in order.
func EqualNodes(a, b []*Node) bool {
	return slices.EqualFunc(a, b, func(x, y *Node) bool {
		return x.Title == y.Title && x.URL == y.URL && x.ID == y.ID && x.M == y.M &&
			x.By == y.By && x.Suffix == y.Suffix && EqualNodes(x.Children, y.Children)
	})
}
