// Package imagetest checks the images under images/: that their
// Containerfiles follow the repository's conventions, and, when a registry
// holding the built images is named, that the built images are what the
// Containerfiles declare.
package imagetest

import (
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// imagesDir is the images/ directory, relative to this package.
const imagesDir = "../../images"

// copyInstr is one COPY instruction.
type copyInstr struct {
	chmod string // the --chmod value, "" without
	src   string
	dst   string
	// uncreated lists the parent directories of dst that no earlier WORKDIR
	// or 0755 COPY of the same Containerfile creates.
	uncreated []string
}

// ancestors returns the directories above p ("/a/b/c" gives "/a", "/a/b"),
// and p itself when self is set.
func ancestors(p string, self bool) []string {
	var out []string
	p = pathpkg.Clean(p)
	for d := pathpkg.Dir(p); d != "/" && d != "."; d = pathpkg.Dir(d) {
		out = append([]string{d}, out...)
	}
	if self && p != "/" {
		out = append(out, p)
	}
	return out
}

// containerfile is the part of a Containerfile the checks need. The
// repository's Containerfiles are simple on purpose: one instruction per
// logical line, LABELs written as LABEL key="value", no multi-stage builds.
type containerfile struct {
	path         string
	from         []string
	args         map[string]string // ARG name -> default
	labels       map[string]string // LABEL key -> value, with ${...} left as written
	copies       []copyInstr
	instructions []string // every instruction name, upper case, in order
	comments     []string
}

var (
	labelRe = regexp.MustCompile(`^LABEL\s+([^=\s]+)="(.*)"\s*$`)
	argRe   = regexp.MustCompile(`^ARG\s+([A-Za-z_][A-Za-z0-9_]*)(?:=(.*))?$`)
)

// parseContainerfile reads and parses path.
func parseContainerfile(t *testing.T, path string) *containerfile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cf := &containerfile{path: path, args: map[string]string{}, labels: map[string]string{}}
	var pending string
	created := map[string]bool{} // directories earlier instructions create with mode 0755
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			cf.comments = append(cf.comments, line)
			continue
		}
		if pending == "" && line == "" {
			continue
		}
		if strings.HasSuffix(line, `\`) {
			pending += strings.TrimSuffix(line, `\`) + " "
			continue
		}
		line = strings.TrimSpace(pending + line)
		pending = ""
		fields := strings.Fields(line)
		instr := strings.ToUpper(fields[0])
		cf.instructions = append(cf.instructions, instr)
		where := fmt.Sprintf("%s:%d", path, i+1)
		switch instr {
		case "FROM":
			cf.from = append(cf.from, fields[1:]...)
		case "ARG":
			m := argRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("%s: unsupported ARG form %q", where, line)
			}
			cf.args[m[1]] = m[2]
		case "LABEL":
			m := labelRe.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("%s: write labels as LABEL key=\"value\", one per instruction: %q", where, line)
			}
			cf.labels[m[1]] = m[2]
		case "WORKDIR":
			if len(fields) != 2 || !pathpkg.IsAbs(fields[1]) {
				t.Fatalf("%s: WORKDIR takes one absolute path: %q", where, line)
			}
			for _, d := range ancestors(fields[1], true) {
				created[d] = true
			}
		case "COPY":
			var c copyInstr
			var rest []string
			for _, f := range fields[1:] {
				if v, ok := strings.CutPrefix(f, "--chmod="); ok {
					c.chmod = v
				} else if strings.HasPrefix(f, "--") {
					t.Fatalf("%s: unsupported COPY flag %q", where, f)
				} else {
					rest = append(rest, f)
				}
			}
			if len(rest) != 2 {
				t.Fatalf("%s: COPY needs exactly one source and a destination: %q", where, line)
			}
			c.src, c.dst = rest[0], rest[1]
			// BuildKit creates the missing parents of a COPY destination
			// with the COPY's own --chmod, so only a 0755 COPY or a
			// WORKDIR may create directories.
			parents := ancestors(c.dst, strings.HasSuffix(c.dst, "/"))
			if c.chmod == "0755" {
				for _, d := range parents {
					created[d] = true
				}
			} else {
				for _, d := range parents {
					if !created[d] {
						c.uncreated = append(c.uncreated, d)
					}
				}
			}
			cf.copies = append(cf.copies, c)
		}
	}
	return cf
}

// variantArg returns the name of the build argument that carries the
// variant image's reference in the base image.
func variantArg(variant string) string {
	return strings.ToUpper(strings.ReplaceAll(variant, "-", "_")) + "_IMAGE"
}

// imageSet is one images/<name>/ directory.
type imageSet struct {
	name     string
	dir      string
	base     *containerfile
	variants []variantImage
}

type variantImage struct {
	name string // the variant's directory name
	dir  string
	cf   *containerfile
}

// repoName is the variant's image name.
func (s *imageSet) repoName(v variantImage) string { return s.name + "-" + v.name }

// loadImages parses every image directory.
func loadImages(t *testing.T) []*imageSet {
	t.Helper()
	entries, err := os.ReadDir(imagesDir)
	if err != nil {
		t.Fatal(err)
	}
	var sets []*imageSet
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s := &imageSet{name: e.Name(), dir: filepath.Join(imagesDir, e.Name())}
		s.base = parseContainerfile(t, filepath.Join(s.dir, "Containerfile"))
		subs, err := os.ReadDir(s.dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, sub := range subs {
			cfPath := filepath.Join(s.dir, sub.Name(), "Containerfile")
			if !sub.IsDir() {
				continue
			}
			if _, err := os.Stat(cfPath); err != nil {
				continue
			}
			s.variants = append(s.variants, variantImage{
				name: sub.Name(),
				dir:  filepath.Join(s.dir, sub.Name()),
				cf:   parseContainerfile(t, cfPath),
			})
		}
		sets = append(sets, s)
	}
	if len(sets) == 0 {
		t.Fatalf("no image directories under %s", imagesDir)
	}
	return sets
}
