package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// specFile is the name of the declarative spec inside an image directory.
const specFile = "image.json"

// defaultSource and defaultLicenses are the org.opencontainers.image.source
// and .licenses values used when a spec does not set its own.
const (
	defaultSource   = "https://github.com/contemper-project/support-images"
	defaultLicenses = "Apache-2.0"
)

// namePattern is what contemper accepts for branch and variant names.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// spec describes one support image: a base image whose annotations point
// at one image per variant of each branch. It is read from
// <image dir>/image.json; the image's name is the directory's name.
type spec struct {
	// Title is the base image's org.opencontainers.image.title. Defaults
	// to the image name.
	Title string `json:"title"`
	// Description is the base image's org.opencontainers.image.description.
	Description string `json:"description"`
	// Source and Licenses override defaultSource and defaultLicenses.
	Source   string `json:"source"`
	Licenses string `json:"licenses"`
	// RequiresFiles becomes io.contemper.requires.files on the base image.
	RequiresFiles []string `json:"requires_files"`
	// Base is the directory (relative to the image directory) whose files
	// make up the base image's layer. Optional: without it the base image
	// carries only annotations, in a single empty layer.
	Base string `json:"base"`
	// Branches are processed, and their variants built, in this order.
	Branches []branch `json:"branches"`
}

// branch is one io.contemper.branch.<name>.* group.
type branch struct {
	Name string `json:"name"`
	// Default names the variant that applies when no predicate matches.
	// Empty means the conversion fails when nothing matches.
	Default  string    `json:"default"`
	Variants []variant `json:"variants"`
}

// variant is one alternative within a branch.
type variant struct {
	Name string `json:"name"`
	// Dir holds the variant image's files, relative to the image
	// directory. A variant without a Dir is a no-op: it has no image and
	// contributes nothing when it wins.
	Dir string `json:"dir"`
	// ImageSuffix is appended to the image name to name the variant
	// image's repository. Required when Dir is set.
	ImageSuffix string `json:"image_suffix"`
	// Title defaults to the variant image's name.
	Title       string `json:"title"`
	Description string `json:"description"`
	// RequiresFiles is the predicate: all of these paths must exist in
	// the source image. Required unless the variant is the branch default.
	RequiresFiles []string `json:"requires_files"`
}

// loadSpec reads and validates dir/image.json.
func loadSpec(dir string) (*spec, error) {
	path := filepath.Join(dir, specFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s spec
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("parsing %s: unexpected data after the top-level object", path)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

func (s *spec) validate() error {
	if s.Description == "" {
		return fmt.Errorf("description is required")
	}
	if s.Base != "" {
		if err := checkLocalDir("base", s.Base); err != nil {
			return err
		}
	}
	if err := checkPaths("requires_files", s.RequiresFiles); err != nil {
		return err
	}
	branches := map[string]bool{}
	suffixes := map[string]bool{}
	for _, b := range s.Branches {
		if !namePattern.MatchString(b.Name) {
			return fmt.Errorf("branch name %q must match %s", b.Name, namePattern)
		}
		if branches[b.Name] {
			return fmt.Errorf("duplicate branch %q", b.Name)
		}
		branches[b.Name] = true
		if len(b.Variants) == 0 {
			return fmt.Errorf("branch %q has no variants", b.Name)
		}
		variants := map[string]bool{}
		for _, v := range b.Variants {
			where := fmt.Sprintf("branch %q variant %q", b.Name, v.Name)
			if !namePattern.MatchString(v.Name) {
				return fmt.Errorf("branch %q: variant name %q must match %s", b.Name, v.Name, namePattern)
			}
			if variants[v.Name] {
				return fmt.Errorf("%s: duplicate variant", where)
			}
			variants[v.Name] = true
			if v.Dir != "" {
				if err := checkLocalDir(where+": dir", v.Dir); err != nil {
					return err
				}
				if v.ImageSuffix == "" {
					return fmt.Errorf("%s: image_suffix is required when dir is set", where)
				}
				if suffixes[v.ImageSuffix] {
					return fmt.Errorf("%s: image_suffix %q is already used", where, v.ImageSuffix)
				}
				suffixes[v.ImageSuffix] = true
			} else if v.ImageSuffix != "" {
				return fmt.Errorf("%s: image_suffix without dir", where)
			}
			if err := checkPaths(where+": requires_files", v.RequiresFiles); err != nil {
				return err
			}
			if len(v.RequiresFiles) == 0 && v.Name != b.Default {
				return fmt.Errorf("%s: requires_files is required unless the variant is the branch default", where)
			}
		}
		if b.Default != "" && !variants[b.Default] {
			return fmt.Errorf("branch %q: default %q is not one of its variants", b.Name, b.Default)
		}
	}
	return nil
}

// checkLocalDir checks that p names a directory strictly inside the image
// directory: relative, no ".." elements.
func checkLocalDir(what, p string) error {
	if p == "" {
		return fmt.Errorf("%s is required", what)
	}
	if !filepath.IsLocal(p) || filepath.Clean(p) == "." {
		return fmt.Errorf("%s %q must be a relative path inside the image directory", what, p)
	}
	return nil
}

// checkPaths checks that every entry is an absolute path without a comma
// (annotations join them with commas).
func checkPaths(what string, paths []string) error {
	for _, p := range paths {
		if !strings.HasPrefix(p, "/") || strings.Contains(p, ",") {
			return fmt.Errorf("%s: %q must be an absolute path without commas", what, p)
		}
	}
	return nil
}
