package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// TestBuildIndexAnnotations checks that buildIndex sets the OCI
// annotations on both the index itself and each platform manifest, while
// leaving the per-descriptor annotations (the ones contemper's own reader
// looks at) exactly as given.
func TestBuildIndexAnnotations(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	descAnnotations := map[string]string{
		"io.contemper.branch.init-system.openrc.requires.files": "/sbin/openrc",
	}
	ociAnns := map[string]string{
		"org.opencontainers.image.title":       "example",
		"org.opencontainers.image.description": "a support image",
		"org.opencontainers.image.source":      defaultSource,
		"org.opencontainers.image.licenses":    defaultLicenses,
	}

	idx, err := buildIndex(dir, descAnnotations, ociAnns)
	if err != nil {
		t.Fatal(err)
	}

	im, err := idx.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}

	for k, want := range ociAnns {
		if got := im.Annotations[k]; got != want {
			t.Errorf("index annotation %q = %q, want %q", k, got, want)
		}
	}

	if len(im.Manifests) != len(platforms) {
		t.Fatalf("got %d manifests, want %d", len(im.Manifests), len(platforms))
	}
	for i, desc := range im.Manifests {
		if desc.Platform == nil || desc.Platform.OS != "linux" || desc.Platform.Architecture != platforms[i] {
			t.Errorf("manifest %d platform = %v, want linux/%s", i, desc.Platform, platforms[i])
		}
		for k, want := range descAnnotations {
			if got := desc.Annotations[k]; got != want {
				t.Errorf("descriptor annotation %q = %q, want %q", k, got, want)
			}
		}
		// The OCI keys must not have leaked into the per-descriptor
		// annotations - they belong on the index and on the platform
		// manifest itself, not here.
		if _, ok := desc.Annotations["org.opencontainers.image.description"]; ok {
			t.Errorf("descriptor annotations unexpectedly carry org.opencontainers.image.description: %v", desc.Annotations)
		}

		img, err := idx.Image(desc.Digest)
		if err != nil {
			t.Fatalf("fetching platform image %s: %v", desc.Digest, err)
		}
		manifest, err := img.Manifest()
		if err != nil {
			t.Fatalf("reading manifest for %s: %v", desc.Digest, err)
		}
		for k, want := range ociAnns {
			if got := manifest.Annotations[k]; got != want {
				t.Errorf("platform manifest annotation %q = %q, want %q", k, got, want)
			}
		}
	}
}

// TestBuildIndexNoOCIAnnotations checks that an empty (or nil) OCI
// annotation map leaves index and manifest annotations untouched rather
// than adding an empty org.opencontainers.* set.
func TestBuildIndexNoOCIAnnotations(t *testing.T) {
	dir := t.TempDir()

	idx, err := buildIndex(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	im, err := idx.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(im.Annotations) != 0 {
		t.Errorf("expected no index annotations, got %v", im.Annotations)
	}
	for _, desc := range im.Manifests {
		img, err := idx.Image(desc.Digest)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := img.Manifest()
		if err != nil {
			t.Fatal(err)
		}
		if len(manifest.Annotations) != 0 {
			t.Errorf("expected no manifest annotations, got %v", manifest.Annotations)
		}
	}
}

// TestOCIAnnotationsRevisionOptional checks that -revision's absence
// simply omits the revision key rather than setting it to "", and that
// it's included when given.
func TestOCIAnnotationsRevisionOptional(t *testing.T) {
	anns := ociAnnotations("example", "", "a description", defaultSource, defaultLicenses)
	if _, ok := anns["org.opencontainers.image.revision"]; ok {
		t.Errorf("expected no revision annotation when revision is empty, got %v", anns)
	}

	anns = ociAnnotations("example", "deadbeef", "a description", defaultSource, defaultLicenses)
	if anns["org.opencontainers.image.revision"] != "deadbeef" {
		t.Errorf("revision annotation = %q, want %q", anns["org.opencontainers.image.revision"], "deadbeef")
	}
}

// TestAppendDigestEmptyPathIsNoop checks that an empty -digests-file (the
// default) writes nothing anywhere.
func TestAppendDigestEmptyPathIsNoop(t *testing.T) {
	if err := appendDigest("", "example.com/img", v1.Hash{}); err != nil {
		t.Fatalf("appendDigest with empty path: %v", err)
	}
}

// TestAppendDigestAppends checks that appendDigest creates the file on its
// first call and appends further "name digest" lines to it afterwards,
// rather than overwriting.
func TestAppendDigestAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "digests.txt")

	h1, err := v1.NewHash("sha256:" + strings.Repeat("1", 64))
	if err != nil {
		t.Fatal(err)
	}
	h2, err := v1.NewHash("sha256:" + strings.Repeat("2", 64))
	if err != nil {
		t.Fatal(err)
	}

	if err := appendDigest(path, "example.com/a", h1); err != nil {
		t.Fatalf("first appendDigest: %v", err)
	}
	if err := appendDigest(path, "example.com/b", h2); err != nil {
		t.Fatalf("second appendDigest: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "example.com/a " + h1.String() + "\n" + "example.com/b " + h2.String() + "\n"
	if string(got) != want {
		t.Errorf("digests file = %q, want %q", got, want)
	}
}

// TestFilesFromDir checks the layer entries a directory turns into:
// parents before children in lexical order, directories with a trailing
// slash, the executable bit mapped to 0755 and everything else to 0644,
// and symlinks carried as symlinks.
func TestFilesFromDir(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "etc", "init.d"))
	mustMkdir(t, filepath.Join(dir, "etc", "runlevels", "default"))
	mustWrite(t, filepath.Join(dir, "etc", "init.d", "svc"), "#!/bin/sh\n", 0o700)
	mustWrite(t, filepath.Join(dir, "etc", "conf"), "x", 0o600)
	if err := os.Symlink("/etc/init.d/svc", filepath.Join(dir, "etc", "runlevels", "default", "svc")); err != nil {
		t.Fatal(err)
	}

	files, err := filesFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		typeflag byte
		mode     int64
		link     string
	}
	want := []struct {
		path string
		entry
	}{
		{"etc/", entry{tar.TypeDir, 0o755, ""}},
		{"etc/conf", entry{tar.TypeReg, 0o644, ""}},
		{"etc/init.d/", entry{tar.TypeDir, 0o755, ""}},
		{"etc/init.d/svc", entry{tar.TypeReg, 0o755, ""}},
		{"etc/runlevels/", entry{tar.TypeDir, 0o755, ""}},
		{"etc/runlevels/default/", entry{tar.TypeDir, 0o755, ""}},
		{"etc/runlevels/default/svc", entry{tar.TypeSymlink, 0o644, "/etc/init.d/svc"}},
	}
	if len(files) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(files), len(want), files)
	}
	for i, w := range want {
		got := files[i]
		if got.path != w.path || got.typeflag != w.typeflag || got.mode != w.mode || got.linkname != w.link {
			t.Errorf("entry %d = {%q %q %o %q}, want {%q %q %o %q}", i,
				got.path, string(got.typeflag), got.mode, got.linkname,
				w.path, string(w.typeflag), w.mode, w.link)
		}
	}
}

// TestLayerIsDeterministic checks that a layer's tar entries are owned by
// root:root and carry the epoch as mtime whatever the files on disk look
// like, so the same content always yields the same digest.
func TestLayerIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a"), "a", 0o644)
	mustWrite(t, filepath.Join(dir, "b"), "b", 0o755)

	files, err := filesFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := buildLayer(files)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := l.Uncompressed()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close() //nolint:errcheck // reading a test layer from memory
	tr := tar.NewReader(rc)
	n := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		n++
		if hdr.Uid != 0 || hdr.Gid != 0 || hdr.Uname != "" || hdr.Gname != "" {
			t.Errorf("%s: owner = %d:%d (%q:%q), want root:root", hdr.Name, hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname)
		}
		if !hdr.ModTime.Equal(epoch) {
			t.Errorf("%s: mtime = %v, want the epoch", hdr.Name, hdr.ModTime)
		}
	}
	if n != 2 {
		t.Errorf("layer has %d entries, want 2", n)
	}

	again, err := buildLayer(files)
	if err != nil {
		t.Fatal(err)
	}
	d1, _ := l.Digest()
	d2, _ := again.Digest()
	if d1 != d2 {
		t.Errorf("same files gave digests %s and %s", d1, d2)
	}
}

// TestBuildPushesEveryTag checks that one build is pushed under each tag,
// all resolving to the same digest, and that the digests file gets one
// line per image (not per tag).
func TestBuildPushesEveryTag(t *testing.T) {
	opts := useRegistry(t)
	imageDir := writeImage(t, "demo", `{
  "description": "demo",
  "base": "base",
  "branches": [{"name": "init-system", "default": "none", "variants": [
    {"name": "openrc", "dir": "openrc", "image_suffix": "-openrc", "description": "o", "requires_files": ["/sbin/openrc"]},
    {"name": "none"}
  ]}]
}`, map[string]string{"base/f": "base", "openrc/g": "openrc"})
	digests := filepath.Join(t.TempDir(), "digests.txt")

	var out bytes.Buffer
	err := run([]string{
		"-prefix", "registry.test/org", "-image-dir", imageDir,
		"-tag", "v1", "-tag", "v1.2.3", "-revision", "abc", "-digests-file", digests,
	}, &out)
	if err != nil {
		t.Fatal(err)
	}

	for _, repo := range []string{"registry.test/org/demo", "registry.test/org/demo-openrc"} {
		a := pulledIndex(t, repo+":v1", opts)
		b := pulledIndex(t, repo+":v1.2.3", opts)
		da, _ := a.Digest()
		db, _ := b.Digest()
		if da != db {
			t.Errorf("%s: tag v1 = %s, v1.2.3 = %s", repo, da, db)
		}
		im, err := a.IndexManifest()
		if err != nil {
			t.Fatal(err)
		}
		if got := im.Annotations["org.opencontainers.image.revision"]; got != "abc" {
			t.Errorf("%s: revision annotation = %q", repo, got)
		}
	}

	data, err := os.ReadFile(digests)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "registry.test/org/demo-openrc sha256:") || !strings.HasPrefix(lines[1], "registry.test/org/demo sha256:") {
		t.Errorf("digests file = %q, want the variant then the base image, one line each", data)
	}
	if n := strings.Count(out.String(), "pushed "); n != 4 {
		t.Errorf("output reports %d pushes, want 4 (two images, two tags):\n%s", n, out.String())
	}

	// The base image carries the branch default, the no-op variant's
	// absence of an image, and the variant pinned by digest.
	base := pulledIndex(t, "registry.test/org/demo:v1", opts)
	im, err := base.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	ann := im.Manifests[0].Annotations
	if ann["io.contemper.branch.init-system.default"] != "none" {
		t.Errorf("default annotation = %q", ann["io.contemper.branch.init-system.default"])
	}
	if _, ok := ann["io.contemper.branch.init-system.none.image"]; ok {
		t.Errorf("no-op variant has an image annotation: %v", ann)
	}
	variant := pulledIndex(t, "registry.test/org/demo-openrc:v1", opts)
	vd, _ := variant.Digest()
	if want := "registry.test/org/demo-openrc@" + vd.String(); ann["io.contemper.branch.init-system.openrc.image"] != want {
		t.Errorf("variant image annotation = %q, want %q", ann["io.contemper.branch.init-system.openrc.image"], want)
	}
}

// TestRunFlagErrors checks the argument validation.
func TestRunFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no prefix", []string{"-image-dir", "x", "-tag", "v1"}, "-prefix is required"},
		{"no image dir", []string{"-prefix", "r.test/o", "-tag", "v1"}, "-image-dir is required"},
		{"no tag", []string{"-prefix", "r.test/o", "-image-dir", "x"}, "at least one -tag"},
		{"bad tag", []string{"-prefix", "r.test/o", "-image-dir", "x", "-tag", "a b"}, "invalid -tag"},
		{"no spec", []string{"-prefix", "r.test/o", "-image-dir", t.TempDir(), "-tag", "v1"}, "image.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

// writeImage creates an image directory named name under a temp dir with
// the given spec and files (paths relative to the image directory), and
// returns its path.
func writeImage(t *testing.T, name, specJSON string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	mustMkdir(t, dir)
	mustWrite(t, filepath.Join(dir, specFile), specJSON, 0o644)
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		mustMkdir(t, filepath.Dir(full))
		mustWrite(t, full, content, 0o644)
	}
	return dir
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile's mode is subject to the umask; set it exactly.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// TestBuildPushesNothingOnFailure checks that a build that fails (here: a
// second variant whose directory is missing) pushes nothing, not even the
// images that built, and that a bad image name is refused up front.
func TestBuildPushesNothingOnFailure(t *testing.T) {
	opts := useRegistry(t)
	imageDir := writeImage(t, "demo", `{
  "description": "demo",
  "base": "base",
  "branches": [{"name": "b", "variants": [
    {"name": "one", "dir": "one", "image_suffix": "-one", "requires_files": ["/a"]},
    {"name": "two", "dir": "missing", "image_suffix": "-two", "requires_files": ["/b"]}
  ]}]
}`, map[string]string{"base/f": "base", "one/g": "one"})
	err := run([]string{"-prefix", "registry.test/org", "-image-dir", imageDir, "-tag", "v1"}, io.Discard)
	if err == nil {
		t.Fatal("expected an error for the missing variant directory")
	}
	r, perr := name.ParseReference("registry.test/org/demo-one:v1")
	if perr != nil {
		t.Fatal(perr)
	}
	if _, gerr := remote.Index(r, opts...); gerr == nil {
		t.Error("demo-one was pushed although the build failed")
	}

	bad := writeImage(t, "Bad_Name", `{"description": "d", "base": "base"}`, map[string]string{"base/f": "x"})
	err = run([]string{"-prefix", "registry.test/org", "-image-dir", bad, "-tag", "v1"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "invalid image name") {
		t.Errorf("err = %v, want an invalid image name error", err)
	}
}

// TestVariantDescriptionOptional checks that a variant without a
// description gets no description annotation instead of an empty one.
func TestVariantDescriptionOptional(t *testing.T) {
	anns := ociAnnotations("t", "", "", defaultSource, defaultLicenses)
	if _, ok := anns["org.opencontainers.image.description"]; ok {
		t.Errorf("empty description was set: %v", anns)
	}
}
