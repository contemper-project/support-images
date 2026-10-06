// Command buildimg builds and pushes a support image and its variant
// images as multi-platform (linux/amd64, linux/arm64) indexes.
//
// An image is a directory images/<name>/ holding an image.json spec and
// the files of its base layer and of each variant, see spec.go. The
// content is architecture-independent (scripts, unit files, symlinks), so
// each platform's image differs only in its config's declared
// architecture, not its files. Layers are deterministic: entries are
// sorted, owned by root:root and carry a fixed modification time, so the
// same files always produce the same digests.
//
// buildimg sets the base image's contemper branch/variant annotations
// with each variant image's reference pinned BY DIGEST (the variant
// index's digest, which resolves to the right platform on read, like any
// other multi-platform reference), never a floating tag, so a support
// image always names an exact, reproducible set of variant bytes.
//
// It also sets the standard org.opencontainers.image.* annotations
// (title, description, source, licenses, and revision when -revision is
// given) on each index and on each platform manifest, so a registry UI
// has something to show for the image. These live in a separate
// namespace from contemper's io.contemper.* keys and are never read by
// contemper.
//
// Every image is built once and pushed under each -tag.
//
// Usage:
//
//	buildimg -prefix ghcr.io/contemper-project -image-dir images/incus-support \
//	    -tag v1 -tag v1.2.3 [-revision <commit-sha>] [-digests-file digests.txt]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// remoteOptions are the options every registry push uses. Tests replace
// them to reach an in-process registry.
var remoteOptions = []remote.Option{remote.WithAuthFromKeychain(authn.DefaultKeychain)}

// platforms is every platform each image is published for, in the fixed
// order they are appended to each index.
var platforms = []string{"amd64", "arm64"}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "buildimg:", err)
		os.Exit(1)
	}
}

// tagList is a repeatable -tag flag.
type tagList []string

func (t *tagList) String() string     { return strings.Join(*t, ",") }
func (t *tagList) Set(v string) error { *t = append(*t, v); return nil }

// options are the settings a build needs besides the spec.
type options struct {
	prefix      string
	imageDir    string
	tags        []string
	revision    string
	digestsFile string
}

func run(args []string, stdout io.Writer) error {
	var opts options
	var tags tagList
	fs := flag.NewFlagSet("buildimg", flag.ContinueOnError)
	fs.StringVar(&opts.prefix, "prefix", "", "registry prefix, e.g. ghcr.io/contemper-project or localhost:5555/test")
	fs.StringVar(&opts.imageDir, "image-dir", "", "image directory, e.g. images/incus-support (holds image.json); the directory's name is the image name")
	fs.Var(&tags, "tag", "tag to publish the images under; repeat the flag to publish under several tags")
	fs.StringVar(&opts.revision, "revision", "", "commit SHA to record as org.opencontainers.image.revision (optional)")
	fs.StringVar(&opts.digestsFile, "digests-file", "", "if set, append one '<image-name> <digest>' line per pushed image to this file (created if missing), for a caller that wants each image's digest without scraping stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts.tags = tags

	if opts.prefix == "" {
		return fmt.Errorf("-prefix is required")
	}
	if opts.imageDir == "" {
		return fmt.Errorf("-image-dir is required")
	}
	if len(opts.tags) == 0 {
		return fmt.Errorf("at least one -tag is required")
	}
	for _, t := range opts.tags {
		if _, err := name.NewTag("example.com/x:" + t); err != nil {
			return fmt.Errorf("invalid -tag %q: %w", t, err)
		}
	}

	s, err := loadSpec(opts.imageDir)
	if err != nil {
		return err
	}
	return build(s, opts, stdout)
}

// built is one image index, built in memory and waiting to be pushed.
type built struct {
	repo   string
	idx    v1.ImageIndex
	digest v1.Hash
}

// build builds every image in memory first - the variant images, then the
// base image whose annotations pin them by digest - and only then pushes
// them, variants before the base, each under every tag. Nothing is pushed
// unless everything built, and every repository name and reference is
// validated before the first push.
func build(s *spec, opts options, stdout io.Writer) error {
	abs, err := filepath.Abs(opts.imageDir)
	if err != nil {
		return err
	}
	imageName := filepath.Base(abs)
	source, licenses := s.Source, s.Licenses
	if source == "" {
		source = defaultSource
	}
	if licenses == "" {
		licenses = defaultLicenses
	}

	baseAnns := map[string]string{}
	if len(s.RequiresFiles) > 0 {
		baseAnns["io.contemper.requires.files"] = strings.Join(s.RequiresFiles, ",")
	}

	var images []built
	for _, b := range s.Branches {
		prefix := "io.contemper.branch." + b.Name + "."
		for _, v := range b.Variants {
			vp := prefix + v.Name + "."
			if len(v.RequiresFiles) > 0 {
				baseAnns[vp+"requires.files"] = strings.Join(v.RequiresFiles, ",")
			}
			if v.Dir == "" {
				continue
			}
			varName := imageName + v.ImageSuffix
			title := v.Title
			if title == "" {
				title = varName
			}
			img, err := buildImageIndex(opts, filepath.Join(abs, v.Dir), opts.prefix+"/"+varName, nil,
				ociAnnotations(title, opts.revision, v.Description, source, licenses))
			if err != nil {
				return fmt.Errorf("%s variant of branch %s: %w", v.Name, b.Name, err)
			}
			images = append(images, img)
			baseAnns[vp+"image"] = img.repo + "@" + img.digest.String()
		}
		if b.Default != "" {
			baseAnns[prefix+"default"] = b.Default
		}
	}

	title := s.Title
	if title == "" {
		title = imageName
	}
	base, err := buildImageIndex(opts, filepath.Join(abs, s.Base), opts.prefix+"/"+imageName, baseAnns,
		ociAnnotations(title, opts.revision, s.Description, source, licenses))
	if err != nil {
		return fmt.Errorf("base image: %w", err)
	}
	images = append(images, base)

	for _, img := range images {
		if err := push(img, opts.tags, stdout); err != nil {
			return err
		}
		if err := appendDigest(opts.digestsFile, img.repo, img.digest); err != nil {
			return fmt.Errorf("recording digest for %s: %w", img.repo, err)
		}
	}
	return nil
}

// appendDigest appends "name digest\n" to path, creating it if needed, so
// a caller (a CI workflow attesting build provenance, e.g.) can read back
// which digest each image name was last pushed at without parsing stdout.
// It does nothing when path is empty.
func appendDigest(path, name string, digest v1.Hash) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // G302: not a secret, just a scratch file this process's own caller reads back
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // best-effort close after a successful write below
	if _, err := fmt.Fprintf(f, "%s %s\n", name, digest); err != nil {
		return fmt.Errorf("writing to %s: %w", path, err)
	}
	return nil
}

// ociAnnotations builds the standard org.opencontainers.image.*
// annotations for one image: its description (one short sentence, since
// ghcr.io shows at most a few hundred characters), source and licenses,
// and title and revision when available. The description is left out
// when empty (a variant's is optional).
func ociAnnotations(title, revision, description, source, licenses string) map[string]string {
	anns := map[string]string{
		"org.opencontainers.image.title":    title,
		"org.opencontainers.image.source":   source,
		"org.opencontainers.image.licenses": licenses,
	}
	if description != "" {
		anns["org.opencontainers.image.description"] = description
	}
	if revision != "" {
		anns["org.opencontainers.image.revision"] = revision
	}
	return anns
}

// buildIndex builds one platform image per entry in platforms from dir's
// file tree and appends each to a multi-platform index, with
// descAnnotations set on every one of its descriptors (the
// index-descriptor annotation fallback contemper reads, which applies
// uniformly regardless of which platform a reader resolves) and
// ociAnns set on both the index itself and each platform manifest - the
// two places ghcr.io reads image metadata from for a multi-platform
// reference. It does no network I/O, so it can be exercised without a
// registry.
func buildIndex(dir string, descAnnotations, ociAnns map[string]string) (v1.ImageIndex, error) {
	files, err := filesFromDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var idx v1.ImageIndex = empty.Index
	for _, arch := range platforms {
		platform := v1.Platform{OS: "linux", Architecture: arch}
		img, err := buildImage(platform, files)
		if err != nil {
			return nil, fmt.Errorf("building %s image: %w", arch, err)
		}
		if len(ociAnns) > 0 {
			img = mutate.Annotations(img, ociAnns).(v1.Image) //nolint:forcetypeassert // mutate.Annotations on a v1.Image always returns a v1.Image
		}
		idx = mutate.AppendManifests(idx, mutate.IndexAddendum{
			Add: img,
			Descriptor: v1.Descriptor{
				Platform:    &platform,
				Annotations: descAnnotations,
			},
		})
	}
	if len(ociAnns) > 0 {
		idx = mutate.Annotations(idx, ociAnns).(v1.ImageIndex) //nolint:forcetypeassert // mutate.Annotations on a v1.ImageIndex always returns a v1.ImageIndex
	}
	return idx, nil
}

// buildImageIndex validates repo and its references under every tag,
// then builds dir's multi-platform index (see buildIndex).
func buildImageIndex(opts options, dir, repo string, descAnnotations, ociAnns map[string]string) (built, error) {
	if _, err := name.NewRepository(repo); err != nil {
		return built{}, fmt.Errorf("invalid image name %q: %w", repo, err)
	}
	for _, tag := range opts.tags {
		if _, err := name.ParseReference(repo + ":" + tag); err != nil {
			return built{}, fmt.Errorf("invalid reference %q: %w", repo+":"+tag, err)
		}
	}
	idx, err := buildIndex(dir, descAnnotations, ociAnns)
	if err != nil {
		return built{}, err
	}
	digest, err := idx.Digest()
	if err != nil {
		return built{}, fmt.Errorf("reading digest of %s: %w", repo, err)
	}
	return built{repo: repo, idx: idx, digest: digest}, nil
}

// push pushes img under every tag; the digest is the same for each.
func push(img built, tags []string, stdout io.Writer) error {
	for _, tag := range tags {
		ref := img.repo + ":" + tag
		nref, err := name.ParseReference(ref)
		if err != nil {
			return fmt.Errorf("parsing %q: %w", ref, err)
		}
		if err := remote.WriteIndex(nref, img.idx, remoteOptions...); err != nil {
			return fmt.Errorf("pushing %s: %w", ref, err)
		}
		if _, err := fmt.Fprintf(stdout, "pushed %s@%s\n", ref, img.digest); err != nil {
			return err
		}
	}
	return nil
}
