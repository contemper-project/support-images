package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSpecValid(t *testing.T) {
	dir := t.TempDir()
	const spec = `{"description": "d", "requires_files": ["/etc/os-release"], "base": "base", "branches": [
  {"name": "init-system", "variants": [
    {"name": "openrc", "dir": "openrc", "image_suffix": "-openrc", "description": "o", "requires_files": ["/sbin/openrc"]},
    {"name": "systemd", "dir": "systemd", "image_suffix": "-systemd", "description": "s", "requires_files": ["/usr/lib/systemd/systemd"]}]}]}`
	if err := os.WriteFile(filepath.Join(dir, specFile), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := loadSpec(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Base != "base" || len(s.Branches) != 1 || len(s.Branches[0].Variants) != 2 || len(s.RequiresFiles) != 1 {
		t.Errorf("unexpected spec: %+v", s)
	}
}

func TestLoadSpecErrors(t *testing.T) {
	const okVariant = `{"name": "a", "dir": "a", "image_suffix": "-a", "requires_files": ["/x"]}`
	for _, tc := range []struct{ name, json, want string }{
		{"not json", `{`, "parsing"},
		{"unknown field", `{"description": "d", "base": "b", "bogus": 1}`, "bogus"},
		{"trailing data", `{"description": "d", "base": "b"} {}`, "unexpected data"},
		{"no description", `{"base": "b"}`, "description is required"},
		{"no base", `{"description": "d"}`, "base is required"},
		{"base escapes", `{"description": "d", "base": "../x"}`, "inside the image directory"},
		{"base is the image dir", `{"description": "d", "base": "."}`, "inside the image directory"},
		{"base absolute", `{"description": "d", "base": "/x"}`, "inside the image directory"},
		{"relative requires", `{"description": "d", "base": "b", "requires_files": ["x"]}`, "absolute path"},
		{"comma in path", `{"description": "d", "base": "b", "requires_files": ["/a,b"]}`, "without commas"},
		{"bad branch name", `{"description": "d", "base": "b", "branches": [{"name": "Bad", "variants": [` + okVariant + `]}]}`, "branch name"},
		{"duplicate branch", `{"description": "d", "base": "b", "branches": [{"name": "x", "variants": [` + okVariant + `]}, {"name": "x", "variants": [` + okVariant + `]}]}`, "duplicate branch"},
		{"empty branch", `{"description": "d", "base": "b", "branches": [{"name": "x", "variants": []}]}`, "no variants"},
		{"bad variant name", `{"description": "d", "base": "b", "branches": [{"name": "x", "variants": [{"name": "_", "requires_files": ["/x"]}]}]}`, "variant name"},
		{"duplicate variant", `{"description": "d", "base": "b", "branches": [{"name": "x", "variants": [` + okVariant + `, ` + okVariant + `]}]}`, "duplicate variant"},
		{"no suffix", `{"description": "d", "base": "b", "branches": [{"name": "x", "variants": [{"name": "a", "dir": "a", "requires_files": ["/x"]}]}]}`, "image_suffix is required"},
		{"suffix without dir", `{"description": "d", "base": "b", "branches": [{"name": "x", "variants": [{"name": "a", "image_suffix": "-a", "requires_files": ["/x"]}]}]}`, "image_suffix without dir"},
		{"suffix reused", `{"description": "d", "base": "b", "branches": [{"name": "x", "variants": [` + okVariant + `, {"name": "b", "dir": "b", "image_suffix": "-a", "requires_files": ["/y"]}]}]}`, "already used"},
		{"no predicate", `{"description": "d", "base": "b", "branches": [{"name": "x", "variants": [{"name": "a", "dir": "a", "image_suffix": "-a"}]}]}`, "requires_files is required"},
		{"unknown default", `{"description": "d", "base": "b", "branches": [{"name": "x", "default": "zz", "variants": [` + okVariant + `]}]}`, "not one of its variants"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, specFile), []byte(tc.json), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := loadSpec(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// TestLoadSpecDefaultNeedsNoPredicate checks that a branch's default
// variant may omit requires_files, and that a variant without a dir (a
// no-op) is accepted.
func TestLoadSpecDefaultNeedsNoPredicate(t *testing.T) {
	dir := t.TempDir()
	const spec = `{"description": "d", "base": "b", "branches": [{"name": "x", "default": "none", "variants": [
  {"name": "a", "dir": "a", "image_suffix": "-a", "requires_files": ["/x"]},
  {"name": "none"}]}]}`
	if err := os.WriteFile(filepath.Join(dir, specFile), []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSpec(dir); err != nil {
		t.Fatal(err)
	}
}
