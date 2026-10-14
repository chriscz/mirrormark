package db

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// SplitPath splits a config folder path on "/". A backslash escapes the
// next character, so `\/` is a slash inside a title.
func SplitPath(p string) ([]string, error) {
	var segs []string
	var cur strings.Builder
	esc := false
	for _, r := range p {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == '/':
			segs = append(segs, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if esc {
		return nil, fmt.Errorf("path %q ends in a backslash", p)
	}
	segs = append(segs, cur.String())
	for _, s := range segs {
		if s == "" {
			return nil, fmt.Errorf("path %q has an empty folder name", p)
		}
	}
	return segs, nil
}

// LoadConfig reads and validates config.yaml. A missing file is an empty
// config, so a fresh install syncs nothing until the user adds a union.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{Format: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := unmarshalStrict(data, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) Validate() error {
	if c.Format != 1 {
		return fmt.Errorf("unsupported format %d", c.Format)
	}
	byProfile := map[string][][]string{}
	for name, members := range c.Unions {
		if name == "" {
			return errors.New("a union has an empty name")
		}
		for id, path := range members {
			if !ValidID.MatchString(id) {
				return fmt.Errorf("union %s: invalid profile id %q", name, id)
			}
			segs, err := SplitPath(path)
			if err != nil {
				return fmt.Errorf("union %s: %w", name, err)
			}
			byProfile[id] = append(byProfile[id], segs)
		}
	}
	// One profile's member folders may not nest, or one union's edits would
	// be another union's edits too.
	for id, all := range byProfile {
		for i, a := range all {
			for j, b := range all {
				if i != j && len(a) <= len(b) && slices.Equal(a, b[:len(a)]) {
					return fmt.Errorf("profile %s: folder %s is inside or equal to %s", id, strings.Join(b, "/"), strings.Join(a, "/"))
				}
			}
		}
	}
	return nil
}
