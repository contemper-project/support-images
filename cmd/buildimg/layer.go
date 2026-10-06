package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// file is one tar entry of a layer. Every entry is owned by root:root and
// carries the epoch as its modification time, so a layer's bytes depend
// only on the paths, modes, link targets and contents of its files.
type file struct {
	path     string
	typeflag byte // tar.TypeReg, tar.TypeDir or tar.TypeSymlink
	mode     int64
	data     []byte
	linkname string
}

var epoch = time.Unix(0, 0)

// buildLayer builds a layer holding files, in the order given.
func buildLayer(files []file) (v1.Layer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     f.path,
			Typeflag: f.typeflag,
			Mode:     f.mode,
			Linkname: f.linkname,
			Size:     int64(len(f.data)),
			ModTime:  epoch,
		}); err != nil {
			return nil, err
		}
		if len(f.data) > 0 {
			if _, err := tw.Write(f.data); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	// The opener lets a consumer read the layer more than once (to hash
	// it, then to upload it) from the same in-memory bytes.
	data := buf.Bytes()
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
}

// buildImage builds a single-layer image for platform from files.
func buildImage(platform v1.Platform, files []file) (v1.Image, error) {
	l, err := buildLayer(files)
	if err != nil {
		return nil, err
	}
	img, err := mutate.AppendLayers(empty.Image, l)
	if err != nil {
		return nil, err
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()
	cfg.OS = platform.OS
	cfg.Architecture = platform.Architecture
	return mutate.ConfigFile(img, cfg)
}

// filesFromDir walks dir and turns every entry into a file, preserving
// its path relative to dir (parents before children, siblings in lexical
// order), its executable bit (mode 0755 vs 0644), and symlinks (read, not
// followed, so an enablement symlink can be shipped).
func filesFromDir(dir string) ([]file, error) {
	var files []file
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("reading symlink %s: %w", path, err)
			}
			files = append(files, file{path: relSlash, typeflag: tar.TypeSymlink, mode: 0o644, linkname: target})
			return nil
		}

		if d.IsDir() {
			files = append(files, file{path: relSlash + "/", typeflag: tar.TypeDir, mode: 0o755})
			return nil
		}

		data, err := os.ReadFile(path) //nolint:gosec // G122: walks a local image directory the caller names, not an attacker-writable tree
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		mode := int64(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		files = append(files, file{path: relSlash, typeflag: tar.TypeReg, mode: mode, data: data})
		return nil
	})
	return files, err
}
