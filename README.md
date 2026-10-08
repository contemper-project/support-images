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

A support image is described by Containerfiles alone, built like any other
image. Each image lives in its own directory, `images/<name>/`:

```text
images/<name>/
  Containerfile            the base image, published as <name>
  README.md                what the image adds and what it promises
  <variant>/
    Containerfile          a variant image, published as <name>-<variant>
    <files...>             the variant's files, laid out as in the guest's /
```

A subdirectory of `images/<name>/` is a variant image if and only if it
contains a Containerfile; the image names come from this layout and
nowhere else.

The base image is `FROM scratch` and carries labels only. Besides the
standard `org.opencontainers.image.*` labels (title, description, source,
licenses), it has the labels contemper reads to choose a variant, with
one build argument per variant that names the variant image:

```dockerfile
FROM scratch

ARG SYSTEMD_IMAGE=ghcr.io/contemper-project/<name>-systemd:v1

LABEL org.opencontainers.image.title="<name>"
LABEL org.opencontainers.image.description="One sentence for the registry page."
LABEL org.opencontainers.image.source="https://github.com/contemper-project/support-images"
LABEL org.opencontainers.image.licenses="Apache-2.0"

LABEL io.contemper.branch.init-system.systemd.requires.files="/usr/lib/systemd/systemd"
LABEL io.contemper.branch.init-system.systemd.image="${SYSTEMD_IMAGE}"
```

For the labels contemper reads, see [contemper's
documentation](https://contemper-project.github.io/contemper/).
The argument is named after the variant directory, upper-cased with `-`
replaced by `_`, plus `_IMAGE`. Its default is the variant's published
major tag; the release build passes the variant's digest instead, so a
published base image always names exact variant content.

A variant image is `FROM scratch` as well, with its title and description
labels and one `COPY` per file. Every `COPY` sets its mode explicitly
(`--chmod=0755` or `--chmod=0644`) so the image does not depend on the
checkout's umask; a copied directory keeps the symlinks inside it. There
is no `RUN` and no `# syntax=` line: building an image runs nothing and
pulls nothing. Only the base image has contemper labels.

Any image can be built locally like any other, for example with:

```sh
podman build -t incus-support-systemd images/incus-support/systemd
docker build -t incus-support-systemd \
  -f images/incus-support/systemd/Containerfile images/incus-support/systemd
```

The base image builds the same way (`images/incus-support`, with
`--build-arg` for the variants' references if they should not be the
published tags).

## Building the published images

`hack/build-image.sh` builds an image directory with `docker buildx build`
and pushes it:

```sh
hack/build-image.sh --prefix ghcr.io/contemper-project \
  --image-dir images/<name> --tag v1 --tag v1.2.3 \
  --revision "$(git rev-parse HEAD)" --version 1.2.3
```

It needs Docker with Buildx and a builder that can push multi-platform
images (such as one created by `docker buildx create`, which uses the
`docker-container` driver). It pushes one image per variant and then the
base image, each as a multi-platform index (linux/amd64 and linux/arm64)
under every `--tag`. The variants go first; the base image is then built
with each variant's reference pinned by the digest of its index. If the
build fails after the variants were pushed, their tags have moved but the
base image has not; that is harmless, since the base image names digests.

The build is reproducible: file timestamps are set to the commit time of
the revision, so the same files always produce the same digests. The
images carry `org.opencontainers.image.revision`, `.version` and
`.created` labels, and the title, description, source and licenses from
the Containerfiles are also set as annotations on the index, where a
registry displays them. `--digests-file` appends one `<image> <digest>`
line per pushed image, for provenance attestation; `--insecure` allows a
registry without TLS.

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
with signed provenance attestations. Separately, every push to `main`
that passes CI publishes all images under `latest`.

| Tag | Meaning |
| --- | --- |
| `v1` | the newest release of major version 1; moves with every release |
| `v1.N.M` | one release; never changes |
| `latest` | the current state of `main`, unreleased; moves with every push to `main` that passes CI and may change at any time |
| `<commit sha>` | the build of one commit |

contemper's default support image reference uses the major tag (`v1`).
Pin a `v1.N.M` tag, or the digest, when you need reproducible conversions;
don't pin `latest`.
The attestations can be checked with
`gh attestation verify oci://ghcr.io/contemper-project/<image>:v1 --owner contemper-project`.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).

## License

Apache License 2.0, see [LICENSE](LICENSE) and [NOTICE](NOTICE).
