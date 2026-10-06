package main

import (
	"archive/tar"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// entry is one tar entry of an image layer, as the tests inspect it.
type entry struct {
	typeflag byte
	mode     int64
	link     string
	content  string
}

// layerEntries returns the entries of img's layers, keyed by path with
// any leading "/" or "./" removed. It fails the test if a path appears in
// more than one layer: these images have a single layer each.
func layerEntries(t *testing.T, img v1.Image) map[string]entry {
	t.Helper()
	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 {
		t.Fatalf("image has %d layers, want 1", len(layers))
	}
	rc, err := layers[0].Uncompressed()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close() //nolint:errcheck // reading a test layer
	out := map[string]entry{}
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimPrefix(strings.TrimPrefix(hdr.Name, "./"), "/")] = entry{hdr.Typeflag, hdr.Mode, hdr.Linkname, string(data)}
	}
	return out
}

// platformImages returns the image for every platform of idx, keyed by
// "os/arch".
func platformImages(t *testing.T, idx v1.ImageIndex) map[string]v1.Image {
	t.Helper()
	im, err := idx.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]v1.Image{}
	for _, desc := range im.Manifests {
		img, err := idx.Image(desc.Digest)
		if err != nil {
			t.Fatal(err)
		}
		out[desc.Platform.OS+"/"+desc.Platform.Architecture] = img
	}
	return out
}

// TestIncusSupportImage builds images/incus-support and checks what a
// conversion relies on: the annotations that select the variant, the
// variants being pinned by digest, and the files and modes each image
// ships.
func TestIncusSupportImage(t *testing.T) {
	opts := useRegistry(t)
	const prefix = "registry.test/contemper-project"
	if err := run([]string{"-prefix", prefix, "-image-dir", "../../images/incus-support", "-tag", "v1"}, io.Discard); err != nil {
		t.Fatal(err)
	}

	base := pulledIndex(t, prefix+"/incus-support:v1", opts)
	baseManifest, err := base.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}

	// Annotations: on the index's own manifest and, as the fallback
	// contemper reads, on every platform descriptor. The same keys, so
	// check them on both.
	wantAnn := map[string]string{
		"io.contemper.branch.init-system.openrc.requires.files":  "/sbin/openrc",
		"io.contemper.branch.init-system.systemd.requires.files": "/usr/lib/systemd/systemd",
	}
	pinned := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `/incus-support-(openrc|systemd)@sha256:[0-9a-f]{64}$`)
	if len(baseManifest.Manifests) != 2 {
		t.Fatalf("base index has %d manifests, want 2 (amd64, arm64)", len(baseManifest.Manifests))
	}
	for _, desc := range baseManifest.Manifests {
		ann := desc.Annotations
		for k, want := range wantAnn {
			if ann[k] != want {
				t.Errorf("%s: annotation %s = %q, want %q", desc.Platform.Architecture, k, ann[k], want)
			}
		}
		for _, v := range []string{"openrc", "systemd"} {
			ref := ann["io.contemper.branch.init-system."+v+".image"]
			if m := pinned.FindStringSubmatch(ref); m == nil || m[1] != v {
				t.Errorf("%s: %s variant image = %q, want %s/incus-support-%s@sha256:<digest>", desc.Platform.Architecture, v, ref, prefix, v)
			}
		}
		// Like volumes-support, a conversion where no variant matches
		// fails instead of silently adding nothing.
		for k := range ann {
			if strings.HasSuffix(k, ".default") {
				t.Errorf("unexpected default annotation %s", k)
			}
		}
		// No other contemper keys: nothing else is promised.
		for k := range ann {
			if strings.HasPrefix(k, "io.contemper.") && !strings.HasPrefix(k, "io.contemper.branch.init-system.") {
				t.Errorf("unexpected annotation %s", k)
			}
		}
	}

	// The base image has no files: it only carries the annotations.
	for arch, img := range platformImages(t, base) {
		if files := layerEntries(t, img); len(files) != 0 {
			t.Errorf("base %s: unexpected files %v", arch, files)
		}
	}

	// Each variant is pulled by the digest the base image pins, and must
	// be the very index the build pushed under the tag.
	for _, v := range []string{"systemd", "openrc"} {
		ref := baseManifest.Manifests[0].Annotations["io.contemper.branch.init-system."+v+".image"]
		r, err := name.NewDigest(ref)
		if err != nil {
			t.Fatal(err)
		}
		byDigest, err := remote.Index(r, opts...)
		if err != nil {
			t.Fatalf("pulling %s: %v", ref, err)
		}
		tagged := pulledIndex(t, prefix+"/incus-support-"+v+":v1", opts)
		d1, _ := byDigest.Digest()
		d2, _ := tagged.Digest()
		if d1 != d2 {
			t.Errorf("%s: pinned digest %s differs from the tag's %s", v, d1, d2)
		}
		if len(platformImages(t, byDigest)) != 2 {
			t.Errorf("%s variant does not cover two platforms", v)
		}
	}

	// checkSetup checks a copy of the Incus setup script.
	checkSetup := func(t *testing.T, arch string, files map[string]entry, path string) string {
		t.Helper()
		setup := files[path]
		if setup.typeflag != tar.TypeReg || setup.mode != 0o755 {
			t.Errorf("%s: %s = %+v, want a regular file with mode 0755", arch, path, setup)
		}
		if !strings.HasPrefix(setup.content, "#!/bin/sh\n") || !strings.Contains(setup.content, "lxc/incus") ||
			!strings.Contains(setup.content, "release v") || !strings.Contains(setup.content, "/run/incus_agent") {
			t.Errorf("%s: %s lacks the shebang, the origin note or the agent directory", arch, path)
		}
		return setup.content
	}

	var systemdSetup, openrcSetup string

	t.Run("systemd", func(t *testing.T) {
		idx := pulledIndex(t, prefix+"/incus-support-systemd:v1", opts)
		for arch, img := range platformImages(t, idx) {
			files := layerEntries(t, img)
			systemdSetup = checkSetup(t, arch, files, "usr/lib/systemd/incus-agent-setup")
			unit := files["usr/lib/systemd/system/incus-agent.service"]
			rule := files["usr/lib/udev/rules.d/99-incus-agent.rules"]
			if unit.typeflag != tar.TypeReg || unit.mode != 0o644 {
				t.Errorf("%s: unit = %+v, want a regular 0644 file", arch, unit)
			}
			if rule.typeflag != tar.TypeReg || rule.mode != 0o644 {
				t.Errorf("%s: udev rule = %+v, want a regular 0644 file", arch, rule)
			}
			if !strings.Contains(unit.content, "\nExecStartPre=/usr/lib/systemd/incus-agent-setup\n") {
				t.Errorf("%s: unit does not run the setup script shipped next to it:\n%s", arch, unit.content)
			}
			if strings.Contains(unit.content, "TARGET/") {
				t.Errorf("%s: unit still has upstream's TARGET placeholder", arch)
			}
			if !strings.Contains(rule.content, `SYMLINK=="virtio-ports/org.linuxcontainers.incus"`) ||
				!strings.Contains(rule.content, `ENV{SYSTEMD_WANTS}+="incus-agent.service"`) {
				t.Errorf("%s: udev rule does not start the unit for the Incus virtio port:\n%s", arch, rule.content)
			}
			// Started by udev only: no enablement symlink, so a plain
			// QEMU boot never starts the agent.
			for p, e := range files {
				if e.typeflag == tar.TypeSymlink {
					t.Errorf("%s: unexpected symlink %s -> %s", arch, p, e.link)
				}
				if e.typeflag != tar.TypeDir && p != "usr/lib/systemd/incus-agent-setup" &&
					p != "usr/lib/systemd/system/incus-agent.service" && p != "usr/lib/udev/rules.d/99-incus-agent.rules" {
					t.Errorf("%s: unexpected file %s", arch, p)
				}
			}
		}
	})

	t.Run("openrc", func(t *testing.T) {
		idx := pulledIndex(t, prefix+"/incus-support-openrc:v1", opts)
		for arch, img := range platformImages(t, idx) {
			files := layerEntries(t, img)
			openrcSetup = checkSetup(t, arch, files, "usr/local/bin/incus-agent-setup")
			for _, svc := range []string{"incus-agent-setup", "incus-agent"} {
				script := files["etc/init.d/"+svc]
				if script.typeflag != tar.TypeReg || script.mode != 0o755 {
					t.Errorf("%s: %s = %+v, want a regular 0755 file", arch, svc, script)
				}
				if !strings.HasPrefix(script.content, "#!/sbin/openrc-run\n") {
					t.Errorf("%s: %s is not an openrc-run script", arch, svc)
				}
				link := files["etc/runlevels/default/"+svc]
				if link.typeflag != tar.TypeSymlink || link.link != "/etc/init.d/"+svc {
					t.Errorf("%s: runlevel entry for %s = %+v, want a symlink to /etc/init.d/%s", arch, svc, link, svc)
				}
			}
			setup := files["etc/init.d/incus-agent-setup"].content
			for _, want := range []string{"/usr/local/bin/incus-agent-setup", "/sys/class/virtio-ports/*/name", "modprobe virtio_console", "org.linuxcontainers.incus", "/dev/virtio-ports/"} {
				if !strings.Contains(setup, want) {
					t.Errorf("%s: incus-agent-setup service lacks %q", arch, want)
				}
			}
			agent := files["etc/init.d/incus-agent"].content
			for _, want := range []string{"command=/run/incus_agent/incus-agent", "command_background=true", "pidfile=/run/incus-agent.pid",
				`start_stop_daemon_args="--chdir /run/incus_agent"`, "need incus-agent-setup"} {
				if !strings.Contains(agent, want) {
					t.Errorf("%s: incus-agent service lacks %q", arch, want)
				}
			}
			for p, e := range files {
				if e.typeflag == tar.TypeSymlink && !strings.HasPrefix(p, "etc/runlevels/default/") {
					t.Errorf("%s: unexpected symlink %s", arch, p)
				}
			}
		}
	})

	// Both variants carry the same setup script.
	if systemdSetup == "" || systemdSetup != openrcSetup {
		t.Error("the variants' copies of the setup script differ")
	}
}
