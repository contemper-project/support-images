package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestEveryImageIsReleased checks that each image directory under images/
// has a valid spec and its own release-please package, named after the
// directory (the release workflow derives image names and git tags from
// that), and that every release-please package is an image directory.
func TestEveryImageIsReleased(t *testing.T) {
	data, err := os.ReadFile("../../release-please-config.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Packages map[string]struct {
			Component      string `json:"component"`
			InitialVersion string `json:"initial-version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}

	dirs, err := filepath.Glob("../../images/*")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			continue
		}
		name := filepath.Base(dir)
		seen["images/"+name] = true
		if _, err := loadSpec(dir); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		pkg, ok := cfg.Packages["images/"+name]
		if !ok {
			t.Errorf("images/%s has no package in release-please-config.json", name)
			continue
		}
		if pkg.Component != name {
			t.Errorf("images/%s: component = %q, want %q", name, pkg.Component, name)
		}
	}
	for path := range cfg.Packages {
		if !seen[path] {
			t.Errorf("release-please package %s is not an image directory", path)
		}
	}
}
