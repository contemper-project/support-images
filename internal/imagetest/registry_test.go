package imagetest

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// The registry tests check the images hack/build-image.sh pushed. They run
// only when IMAGETEST_REGISTRY names the prefix the images were pushed
// under, e.g. localhost:5000/contemper-project; IMAGETEST_TAG is the tag
// they were pushed with (default "ci").
const (
	registryEnv = "IMAGETEST_REGISTRY"
	tagEnv      = "IMAGETEST_TAG"
)

var wantPlatforms = []string{"linux/amd64", "linux/arm64"}

func registryPrefix(t *testing.T) (prefix, tag string) {
	t.Helper()
	prefix = strings.TrimSuffix(os.Getenv(registryEnv), "/")
	if prefix == "" {
		t.Skipf("%s is not set; no registry with built images to check", registryEnv)
	}
	tag = os.Getenv(tagEnv)
	if tag == "" {
		tag = "ci"
	}
	return prefix, tag
}

var remoteOptions = []remote.Option{remote.WithAuthFromKeychain(authn.DefaultKeychain)}

// pullIndex reads the image index at ref.
func pullIndex(t *testing.T, ref string) v1.ImageIndex {
	t.Helper()
	r, err := name.ParseReference(ref, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := remote.Index(r, remoteOptions...)
	if err != nil {
		t.Fatalf("reading %s: %v", ref, err)
	}
	return idx
}

// platformImages returns the image of every platform of idx, keyed by
// "os/arch", and fails unless these are exactly the published platforms
// (so, among other things, no attestation manifests).
func platformImages(t *testing.T, idx v1.ImageIndex) map[string]v1.Image {
	t.Helper()
	im, err := idx.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]v1.Image{}
	for _, desc := range im.Manifests {
		if desc.Platform == nil {
			t.Fatalf("index entry %s has no platform", desc.Digest)
		}
		img, err := idx.Image(desc.Digest)
		if err != nil {
			t.Fatal(err)
		}
		out[desc.Platform.OS+"/"+desc.Platform.Architecture] = img
	}
	if len(out) != len(wantPlatforms) || len(im.Manifests) != len(wantPlatforms) {
		t.Fatalf("index has %d entries, want exactly %v", len(im.Manifests), wantPlatforms)
	}
	for _, p := range wantPlatforms {
		if out[p] == nil {
			t.Fatalf("index lacks platform %s", p)
		}
	}
	return out
}

// entry is one non-directory tar entry of an image, as the tests inspect it.
type entry struct {
	typeflag byte
	mode     int64
	link     string
	content  string
}

// flatten returns the files of img's layers applied in order, keyed by
// path without a leading "/" or "./". It fails the test on whiteouts and
// on any entry, directories included, not owned by root and on any
// directory whose mode is not 0755.
func flatten(t *testing.T, img v1.Image) map[string]entry {
	t.Helper()
	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]entry{}
	for _, l := range layers {
		rc, err := l.Uncompressed()
		if err != nil {
			t.Fatal(err)
		}
		tr := tar.NewReader(rc)
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			p := strings.TrimPrefix(strings.TrimPrefix(hdr.Name, "./"), "/")
			p = strings.TrimSuffix(p, "/")
			if strings.HasPrefix(path.Base(p), ".wh.") {
				t.Errorf("unexpected whiteout %s", hdr.Name)
			}
			if hdr.Uid != 0 || hdr.Gid != 0 {
				t.Errorf("%s is owned by %d:%d, want root", hdr.Name, hdr.Uid, hdr.Gid)
			}
			if hdr.Typeflag == tar.TypeDir {
				if mode := hdr.FileInfo().Mode().Perm(); mode != 0o755 {
					t.Errorf("directory %s has mode %04o, want 0755", hdr.Name, mode)
				}
				continue
			}
			data, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			out[p] = entry{hdr.Typeflag, int64(hdr.FileInfo().Mode().Perm()), hdr.Linkname, string(data)}
		}
		if err := rc.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func config(t *testing.T, img v1.Image) *v1.ConfigFile {
	t.Helper()
	cf, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	return cf
}

// TestBuiltImages checks every image directory's built images against its
// Containerfiles: the base image's labels with the variants pinned by
// digest in the same repository prefix, both platforms, the labels and
// index annotations a registry shows, and, per variant, exactly the files
// the COPY instructions declare with their modes, root ownership and
// symlink targets.
func TestBuiltImages(t *testing.T) {
	prefix, tag := registryPrefix(t)
	pinned := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `/([a-z0-9-]+)@sha256:[0-9a-f]{64}$`)

	for _, s := range loadImages(t) {
		t.Run(s.name, func(t *testing.T) {
			baseIdx := pullIndex(t, prefix+"/"+s.name+":"+tag)
			checkAnnotations(t, baseIdx, s.base)
			for plat, img := range platformImages(t, baseIdx) {
				labels := config(t, img).Config.Labels
				for k, want := range s.base.labels {
					got := labels[k]
					if !strings.HasPrefix(want, "${") {
						if got != want {
							t.Errorf("%s: label %s = %q, want %q", plat, k, got, want)
						}
						continue
					}
					// A variant reference, pinned by the digest of the
					// variant's index in the same repository prefix.
					var v *variantImage
					for i := range s.variants {
						if "${"+variantArg(s.variants[i].name)+"}" == want {
							v = &s.variants[i]
						}
					}
					m := pinned.FindStringSubmatch(got)
					if v == nil || m == nil || m[1] != s.repoName(*v) {
						t.Errorf("%s: label %s = %q, want %s/<variant image>@sha256:<digest>", plat, k, got, prefix)
						continue
					}
					byDigest := pullIndex(t, got)
					tagged := pullIndex(t, prefix+"/"+s.repoName(*v)+":"+tag)
					d1, _ := byDigest.Digest()
					d2, _ := tagged.Digest()
					if d1 != d2 {
						t.Errorf("%s: %s pins %s, but the tag %s is at %s", plat, k, d1, tag, d2)
					}
				}
				for k := range labels {
					if strings.HasPrefix(k, "io.contemper.") {
						if _, ok := s.base.labels[k]; !ok {
							t.Errorf("%s: unexpected label %s", plat, k)
						}
					}
				}
				if files := flatten(t, img); len(files) != 0 {
					t.Errorf("%s: the base image should have no files, has %v", plat, files)
				}
			}

			for _, v := range s.variants {
				t.Run(v.name, func(t *testing.T) {
					idx := pullIndex(t, prefix+"/"+s.repoName(v)+":"+tag)
					checkAnnotations(t, idx, v.cf)
					want := expectedFiles(t, v)
					for plat, img := range platformImages(t, idx) {
						labels := config(t, img).Config.Labels
						for k, w := range v.cf.labels {
							if labels[k] != w {
								t.Errorf("%s: label %s = %q, want %q", plat, k, labels[k], w)
							}
						}
						for k := range labels {
							if strings.HasPrefix(k, "io.contemper.") {
								t.Errorf("%s: a variant image has the contemper label %s", plat, k)
							}
						}
						got := flatten(t, img)
						for p, w := range want {
							e, ok := got[p]
							switch {
							case !ok:
								t.Errorf("%s: %s is missing", plat, p)
							case w.link != "":
								if e.typeflag != tar.TypeSymlink || e.link != w.link {
									t.Errorf("%s: %s = %+v, want a symlink to %s", plat, p, e, w.link)
								}
							case e.typeflag != tar.TypeReg || e.mode != w.mode:
								t.Errorf("%s: %s = type %c mode %04o, want a regular file with mode %04o", plat, p, e.typeflag, e.mode, w.mode)
							}
						}
						for p := range got {
							if _, ok := want[p]; !ok {
								t.Errorf("%s: unexpected file %s", plat, p)
							}
						}
						if s.name == "incus-support" {
							checkIncusSupport(t, plat, v.name, got)
						}
					}
				})
			}
		})
	}
}

// checkAnnotations checks the index-level annotations registries show.
func checkAnnotations(t *testing.T, idx v1.ImageIndex, cf *containerfile) {
	t.Helper()
	im, err := idx.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"title", "description", "source", "licenses"} {
		key := "org.opencontainers.image." + k
		if got := im.Annotations[key]; got != cf.labels[key] {
			t.Errorf("index annotation %s = %q, want %q", key, got, cf.labels[key])
		}
	}
}

// checkIncusSupport checks the content of the files incus-support ships,
// on top of the generic checks: what the services run and where they look.
func checkIncusSupport(t *testing.T, plat, variant string, files map[string]entry) {
	t.Helper()
	checkSetup := func(p string) {
		setup := files[p]
		if !strings.HasPrefix(setup.content, "#!/bin/sh\n") || !strings.Contains(setup.content, "lxc/incus") ||
			!strings.Contains(setup.content, "release v") || !strings.Contains(setup.content, "/run/incus_agent") {
			t.Errorf("%s: %s lacks the shebang, the origin note or the agent directory", plat, p)
		}
	}

	switch variant {
	case "systemd":
		checkSetup("usr/lib/systemd/incus-agent-setup")
		unit := files["usr/lib/systemd/system/incus-agent.service"].content
		rule := files["usr/lib/udev/rules.d/99-incus-agent.rules"].content
		if !strings.Contains(unit, "\nExecStartPre=/usr/lib/systemd/incus-agent-setup\n") {
			t.Errorf("%s: unit does not run the setup script shipped next to it:\n%s", plat, unit)
		}
		if strings.Contains(unit, "TARGET/") {
			t.Errorf("%s: unit still has upstream's TARGET placeholder", plat)
		}
		if !strings.Contains(rule, `SYMLINK=="virtio-ports/org.linuxcontainers.incus"`) ||
			!strings.Contains(rule, `ENV{SYSTEMD_WANTS}+="incus-agent.service"`) {
			t.Errorf("%s: udev rule does not start the unit for the Incus virtio port:\n%s", plat, rule)
		}
		// Started by udev only: no enablement symlink, so a plain QEMU
		// boot never starts the agent.
		for p, e := range files {
			if e.typeflag == tar.TypeSymlink {
				t.Errorf("%s: unexpected symlink %s -> %s", plat, p, e.link)
			}
		}
	case "openrc":
		checkSetup("usr/local/bin/incus-agent-setup")
		for _, svc := range []string{"incus-agent-setup", "incus-agent"} {
			script := files["etc/init.d/"+svc]
			if !strings.HasPrefix(script.content, "#!/sbin/openrc-run\n") {
				t.Errorf("%s: %s is not an openrc-run script", plat, svc)
			}
			link := files["etc/runlevels/default/"+svc]
			if link.typeflag != tar.TypeSymlink || link.link != "/etc/init.d/"+svc {
				t.Errorf("%s: runlevel entry for %s = %+v, want a symlink to /etc/init.d/%s", plat, svc, link, svc)
			}
		}
		setup := files["etc/init.d/incus-agent-setup"].content
		for _, want := range []string{"/usr/local/bin/incus-agent-setup", "/sys/class/virtio-ports/*/name", "modprobe virtio_console", "org.linuxcontainers.incus", "/dev/virtio-ports/"} {
			if !strings.Contains(setup, want) {
				t.Errorf("%s: incus-agent-setup service lacks %q", plat, want)
			}
		}
		agent := files["etc/init.d/incus-agent"].content
		for _, want := range []string{"command=/run/incus_agent/incus-agent", "command_background=true", "pidfile=/run/incus-agent.pid",
			`start_stop_daemon_args="--chdir /run/incus_agent"`, "need incus-agent-setup"} {
			if !strings.Contains(agent, want) {
				t.Errorf("%s: incus-agent service lacks %q", plat, want)
			}
		}
		for p, e := range files {
			if e.typeflag == tar.TypeSymlink && !strings.HasPrefix(p, "etc/runlevels/default/") {
				t.Errorf("%s: unexpected symlink %s", plat, p)
			}
		}
	}
}

// expectedFile is one non-directory entry a variant image must ship.
type expectedFile struct {
	mode int64  // permission bits of a regular file
	link string // target of a symlink, "" for a regular file
}

// expectedFiles works out, from the COPY instructions and the files they
// name, every file and symlink the variant's layers must hold, keyed by
// path without the leading slash.
func expectedFiles(t *testing.T, v variantImage) map[string]expectedFile {
	t.Helper()
	out := map[string]expectedFile{}
	for _, c := range v.cf.copies {
		mode, err := strconv.ParseInt(c.chmod, 8, 32)
		if err != nil {
			t.Fatalf("%s: COPY %s: bad --chmod %q", v.dir, c.src, c.chmod)
		}
		src := filepath.Join(v.dir, c.src)
		dst := strings.TrimPrefix(c.dst, "/")
		info, err := os.Lstat(src)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			if strings.HasSuffix(c.dst, "/") {
				dst = filepath.ToSlash(filepath.Join(dst, filepath.Base(src)))
			}
			out[dst] = expectedFile{mode: mode}
			continue
		}
		err = filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(src, p)
			if err != nil {
				return err
			}
			target := filepath.ToSlash(filepath.Join(dst, rel))
			switch {
			case d.IsDir():
			case d.Type()&os.ModeSymlink != 0:
				link, err := os.Readlink(p)
				if err != nil {
					return err
				}
				out[target] = expectedFile{link: link}
			default:
				out[target] = expectedFile{mode: mode}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}
