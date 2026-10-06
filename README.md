# contemper support images

Support images for [contemper](https://github.com/contemper-project/contemper).

A support image is a small OCI image that contemper merges into an image
it converts, adding what a specific virtualization target needs inside
the guest, such as an agent and the init-system units that start it.
contemper picks the right variant for the image's init system at
conversion time.

The images are published to `ghcr.io/contemper-project`. Each image has
a moving major tag (`v1`) and an immutable tag per release (`v1.N.M`).
The major version tracks the interface between contemper and the
support image, not the version of the software the image supports: a
new major version means contemper needs a matching release to use it.

## Layout

Each image lives in its own directory, `images/<name>/`:

```text
images/<name>/
  image.json        the spec: what to build and how the variants are chosen
  README.md         what the image adds and what it promises
  base/             files of the base image, laid out as in the guest's /
  <variant>/        files of one variant image, laid out as in the guest's /
```

The spec names the base directory and, per branch, its variants. A
variant has a directory, a name suffix for its image, and the files that
must exist in the image being converted for it to apply. For the
schema contemper reads back, see its documentation on [support image
annotations](https://contemper-project.github.io/contemper/reference/support-image-annotations/).

```json
{
  "description": "One sentence for the registry page.",
  "base": "base",
  "branches": [
    {
      "name": "init-system",
      "variants": [
        {
          "name": "systemd",
          "dir": "systemd",
          "image_suffix": "-systemd",
          "description": "One sentence for the registry page.",
          "requires_files": ["/usr/lib/systemd/systemd"]
        }
      ]
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `description` | the base image's description; `title`, `source` and `licenses` may override the other OCI annotations (they default to the image name, this repository and Apache-2.0) |
| `base` | directory with the base image's files |
| `requires_files` | optional; paths that must exist in the final image |
| `branches[].name` | the branch, for example `init-system` |
| `branches[].default` | optional; the variant that applies when no predicate matches. Without it, a conversion where nothing matches fails |
| `variants[].name`, `.dir`, `.image_suffix` | the variant, its files, and the suffix that names its image (`<name><suffix>`). A variant without a `dir` is a no-op: it has no image and adds nothing |
| `variants[].requires_files` | the predicate: paths that must all exist in the image being converted. Required unless the variant is the branch default |

Paths in file directories are relative to the guest's root; executable
files become mode 0755, other files 0644, and symlinks stay symlinks.

## Building

`cmd/buildimg` builds an image from its directory and pushes it:

```sh
go run ./cmd/buildimg -prefix ghcr.io/contemper-project \
  -image-dir images/<name> -tag v1 -tag v1.2.3 -revision "$(git rev-parse HEAD)"
```

It pushes the base image and one image per variant, each as a
multi-platform index (linux/amd64 and linux/arm64) under every `-tag`.
Layers are deterministic (sorted entries, owned by root, fixed
timestamps), so the same files always produce the same digests. The
base image names each variant image by digest, never by tag, so it
always refers to exact variant content. `-digests-file` appends one
`<image> <digest>` line per pushed image, for provenance attestation.

## Releases

Every image is versioned and released on its own with
[release-please](https://github.com/googleapis/release-please), from the
[Conventional Commits](https://www.conventionalcommits.org/). It assigns
a commit to an image by the paths the commit changes under that image's
directory; using the image name as the commit scope is only a
convention. A release pull
request per image collects the changes; merging it creates the git tag
`<name>-v<version>` (for example `incus-support-v1.0.0`) and a GitHub
release, and then the release workflow builds the image and publishes it
with signed provenance attestations.

| Tag | Meaning |
| --- | --- |
| `v1` | the newest release of major version 1; moves with every release |
| `v1.N.M` | one release; never changes |
| `<commit sha>` | the build of one commit |

Pin a `v1.N.M` tag, or the digest, when you need reproducible conversions.
The attestations can be checked with
`gh attestation verify oci://ghcr.io/contemper-project/<image>:v1 --owner contemper-project`.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).

## License

Apache License 2.0, see [LICENSE](LICENSE) and [NOTICE](NOTICE).
