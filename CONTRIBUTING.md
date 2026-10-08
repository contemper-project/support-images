# Contributing

Thanks for considering a contribution to the contemper support images.

## Reporting bugs and requesting features

Issues for this repository are tracked in the
[contemper issue tracker](https://github.com/contemper-project/contemper/issues)
and its [project](https://github.com/orgs/contemper-project/projects/1);
please open them there and name the image you mean. Search for an
existing issue first. Security vulnerabilities are the one exception:
report those privately, following [SECURITY.md](SECURITY.md), never as a
public issue.

## Building and testing

You need the Go version in `go.mod` and, for linting,
[shellcheck](https://www.shellcheck.net/).

```sh
go vet ./...
go test ./...
```

`make test` does the same. Run the linters before opening a PR:

```sh
make lint
```

This downloads the [golangci-lint](https://golangci-lint.run/) version
pinned in `.golangci-lint-version` (the same one CI uses) into `bin/` on
first run, runs it with the configuration in `.golangci.yml`, runs
[actionlint](https://github.com/rhysd/actionlint) on the workflows and
shellchecks every shell and OpenRC script under `hack/` and `images/`.

The tests in `internal/imagetest` check the layout of `images/` without
building anything: the Containerfiles, their labels, the build argument and
label that tie each variant to the base image, and that every `COPY`
source exists and every file of a variant is copied. When a registry holding
the built images is named, they also check the built images:

```sh
IMAGETEST_REGISTRY=localhost:5000/contemper-project IMAGETEST_TAG=ci \
  go test ./internal/imagetest
```

CI builds every image into a local registry with `hack/build-image.sh`,
builds again to check the digests are the same, and runs these tests
against the result. When you add or change an image, change it by editing
its Containerfiles and files (see the README for the layout) and keep these
checks passing; extend them when the change adds something they do not
cover.

### Generated files

Some image files are generated, not written by hand: the Incus agent
files of `images/incus-support` come from the Incus release pinned in
`images/incus-support/incus-agent.pin`. After changing the pin, run
`./hack/sync-incus-agent.sh` and commit the result; CI fails if the files
differ from what the script produces.

## Commit style

Commits follow [Conventional Commits](https://www.conventionalcommits.org/):
a subject line of `type(scope): summary`, using one of `feat`, `fix`,
`perf`, `deps`, `docs`, `refactor`, `test`, `build`, `ci`, `chore` or
`revert` as the type, and the affected image or area as the scope, e.g.
`fix(incus-support): start the agent after local filesystems are
mounted` or `ci(actions): update the pinned actions`. Keep commits small
and focused, one logical change each, with imperative-mood subjects. A CI
check enforces the format.

PRs are rebase-merged, so every commit in a PR ends up on `main`
individually and must follow this style on its own. Each image is
released separately by
[release-please](https://github.com/googleapis/release-please): a commit
counts toward an image's next release and changelog when it changes files
under that image's directory in `images/`. Keep a commit to one image,
and name the image as its scope, so the history reads the same way. You
don't need to bump versions or write changelog entries by hand.

## License

By contributing, you agree your contribution is licensed under the
Apache License 2.0 (see [LICENSE](LICENSE)), on the same terms as the
rest of the project.

### Developer Certificate of Origin

Every commit must carry a `Signed-off-by` trailer. Signing off certifies
you wrote the change or otherwise have the right to submit it under the
project's license, per the
[Developer Certificate of Origin](https://developercertificate.org/);
there's no separate CLA to sign.

Add the trailer by committing with `git commit -s` (or `--signoff`),
using your configured `user.name` and `user.email`:

```sh
git commit -s -m "fix(incus-support): start the agent after local filesystems are mounted"
```

A CI check rejects any commit in a PR that's missing one, or whose
trailer doesn't match its author. If that happens, sign off the
existing commits and force-push:

```sh
git rebase --signoff origin/main
git push --force-with-lease
```
