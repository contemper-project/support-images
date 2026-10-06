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

## License

Apache License 2.0, see [LICENSE](LICENSE) and [NOTICE](NOTICE).
