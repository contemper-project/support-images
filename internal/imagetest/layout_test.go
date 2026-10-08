package imagetest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// TestContainerfileLayout checks what the build script and contemper rely
// on, without building anything: the layout, the labels, the build
// arguments that tie the base image to its variants, and that the COPY
// instructions name files that exist and cover every file of a variant.
func TestContainerfileLayout(t *testing.T) {
	for _, s := range loadImages(t) {
		t.Run(s.name, func(t *testing.T) {
			if !namePattern.MatchString(s.name) {
				t.Errorf("image name %q must match %s", s.name, namePattern)
			}
			checkCommon(t, s.base, s.name)
			if len(s.base.copies) != 0 {
				t.Errorf("%s: the base image carries labels only, it has COPY instructions", s.base.path)
			}

			// Every variant directory is declared by the base image: a
			// build argument naming its image and a contemper label
			// that uses it.
			used := map[string]bool{}
			for k, v := range s.base.labels {
				if strings.HasPrefix(k, "io.contemper.") && strings.HasSuffix(k, ".image") {
					used[v] = true
				}
			}
			for _, v := range s.variants {
				if !namePattern.MatchString(v.name) {
					t.Errorf("variant name %q must match %s", v.name, namePattern)
				}
				arg := variantArg(v.name)
				def, ok := s.base.args[arg]
				if !ok {
					t.Errorf("%s: no ARG %s for the variant %s", s.base.path, arg, v.name)
				} else if !strings.HasPrefix(def, "ghcr.io/contemper-project/"+s.repoName(v)+":") {
					t.Errorf("%s: ARG %s defaults to %q, want a tag of ghcr.io/contemper-project/%s", s.base.path, arg, def, s.repoName(v))
				}
				ref := "${" + arg + "}"
				found := false
				for k, val := range s.base.labels {
					if val == ref && strings.HasSuffix(k, "."+v.name+".image") && strings.HasPrefix(k, "io.contemper.branch.") {
						found = true
					}
				}
				if !found {
					t.Errorf("%s: no io.contemper.branch.<branch>.%s.image label set to %q", s.base.path, v.name, ref)
				}
				delete(used, ref)

				checkCommon(t, v.cf, s.repoName(v))
				for k := range v.cf.labels {
					if strings.HasPrefix(k, "io.contemper.") {
						t.Errorf("%s: only the base image carries contemper labels, found %s", v.cf.path, k)
					}
				}
				checkVariantFiles(t, v)
			}
			for ref := range used {
				t.Errorf("%s: a label uses %s, which is not a variant directory's image", s.base.path, ref)
			}
			// ARGs without a variant directory would be dead weight.
			for arg := range s.base.args {
				found := false
				for _, v := range s.variants {
					found = found || variantArg(v.name) == arg
				}
				if !found {
					t.Errorf("%s: ARG %s has no variant directory", s.base.path, arg)
				}
			}
		})
	}
}

// checkCommon checks what every Containerfile of this repository has to
// satisfy: it builds from scratch with no RUN and no syntax frontend, and
// carries the standard labels.
func checkCommon(t *testing.T, cf *containerfile, wantTitle string) {
	t.Helper()
	if len(cf.from) != 1 || cf.from[0] != "scratch" {
		t.Errorf("%s: want FROM scratch, got FROM %v", cf.path, cf.from)
	}
	for _, in := range cf.instructions {
		switch in {
		case "FROM", "ARG", "LABEL", "COPY", "WORKDIR":
		default:
			t.Errorf("%s: unexpected %s instruction; images are built from scratch with COPY, LABEL and WORKDIR only", cf.path, in)
		}
	}
	for _, c := range cf.comments {
		if regexp.MustCompile(`(?i)^#\s*(syntax|escape)\s*=`).MatchString(c) {
			t.Errorf("%s: no parser directives (a syntax line pulls a frontend image): %s", cf.path, c)
		}
	}
	for _, k := range []string{"title", "description", "source", "licenses"} {
		if cf.labels["org.opencontainers.image."+k] == "" {
			t.Errorf("%s: missing label org.opencontainers.image.%s", cf.path, k)
		}
	}
	if got := cf.labels["org.opencontainers.image.title"]; got != wantTitle {
		t.Errorf("%s: title = %q, want %q", cf.path, got, wantTitle)
	}
}

// checkVariantFiles checks a variant's COPY instructions: sources exist
// inside the variant directory, modes are explicit, and every file in the
// directory is shipped by some instruction (nothing is left out
// unnoticed).
func checkVariantFiles(t *testing.T, v variantImage) {
	t.Helper()
	covered := map[string]bool{}
	for _, c := range v.cf.copies {
		if c.chmod != "0644" && c.chmod != "0755" {
			t.Errorf("%s: COPY %s needs --chmod=0644 or --chmod=0755, has %q", v.cf.path, c.src, c.chmod)
		}
		if c.chmod != "0755" && len(c.uncreated) > 0 {
			t.Errorf("%s: COPY %s creates the missing parents %v with mode %s; create them first with WORKDIR (mode 0755)", v.cf.path, c.src, c.uncreated, c.chmod)
		}
		if !filepath.IsLocal(c.src) || !strings.HasPrefix(c.dst, "/") {
			t.Errorf("%s: COPY %s %s: want a local source and an absolute destination", v.cf.path, c.src, c.dst)
			continue
		}
		src := filepath.Join(v.dir, c.src)
		info, err := os.Lstat(src)
		if err != nil {
			t.Errorf("%s: COPY source %s: %v", v.cf.path, c.src, err)
			continue
		}
		if !info.IsDir() {
			covered[filepath.Clean(c.src)] = true
			continue
		}
		err = filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(v.dir, p)
				covered[rel] = true
			}
			return err
		})
		if err != nil {
			t.Error(err)
		}
	}
	err := filepath.WalkDir(v.dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(v.dir, p)
		if rel != "Containerfile" && !covered[rel] {
			t.Errorf("%s: %s is not copied by any COPY instruction", v.cf.path, rel)
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
}

// TestEveryImageIsReleased checks that each image directory under images/
// has its own release-please package, named after the directory (the
// release workflow derives image names and git tags from that), and that
// every release-please package is an image directory.
func TestEveryImageIsReleased(t *testing.T) {
	data, err := os.ReadFile("../../release-please-config.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Packages map[string]struct {
			Component string `json:"component"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range loadImages(t) {
		seen["images/"+s.name] = true
		pkg, ok := cfg.Packages["images/"+s.name]
		if !ok {
			t.Errorf("images/%s has no package in release-please-config.json", s.name)
			continue
		}
		if pkg.Component != s.name {
			t.Errorf("images/%s: component = %q, want %q", s.name, pkg.Component, s.name)
		}
	}
	for path := range cfg.Packages {
		if !seen[path] {
			t.Errorf("release-please package %s is not an image directory", path)
		}
	}
}

// TestImageList checks that hack/image-list.sh derives the image names
// from the layout, the names the build script pushes.
func TestImageList(t *testing.T) {
	sets := loadImages(t)
	var args []string
	var want []map[string]string
	for _, s := range sets {
		args = append(args, filepath.Join(imagesDir, s.name))
		want = append(want, map[string]string{"component": s.name, "image": s.name})
		for _, v := range s.variants {
			want = append(want, map[string]string{"component": s.name, "image": s.repoName(v)})
		}
	}
	out, err := exec.CommandContext(t.Context(), "sh", append([]string{"../../hack/image-list.sh"}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("image-list.sh printed %q: %v", out, err)
	}
	key := func(l []map[string]string) {
		sort.Slice(l, func(i, j int) bool { return l[i]["image"] < l[j]["image"] })
	}
	key(got)
	key(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("image-list.sh = %v, want %v", got, want)
	}
}
