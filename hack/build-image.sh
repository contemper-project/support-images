#!/usr/bin/env bash
# Builds an image directory with `docker buildx build` and pushes every image
# in it as a multi-platform (linux/amd64, linux/arm64) index.
#
# An image directory images/<name>/ holds a Containerfile for the base image
# <name> and, in each subdirectory that has a Containerfile of its own, a
# variant image <name>-<variant>. The content is architecture-independent
# (files copied into an empty image), so every platform's image has the same
# layers.
#
# Usage:
#   hack/build-image.sh --prefix ghcr.io/contemper-project \
#       --image-dir images/incus-support --tag v1 --tag v1.2.3 \
#       [--revision <commit>] [--version 1.2.3] [--digests-file digests.txt] \
#       [--insecure] [--no-cache]
#
#   --prefix        registry and namespace to push to, e.g.
#                   ghcr.io/contemper-project or localhost:5000/test
#   --image-dir     the image directory; its name is the base image's name
#   --tag           tag to push every image under; repeat for several tags.
#                   Each image is built once and pushed under all of them
#   --revision      commit the images are built from: recorded as
#                   org.opencontainers.image.revision, and its commit time is
#                   the build's SOURCE_DATE_EPOCH (HEAD's without it)
#   --version       recorded as org.opencontainers.image.version
#   --digests-file  append one '<repository> <digest>' line per pushed image
#   --insecure      allow pushing to a registry without TLS
#   --no-cache      build without BuildKit's cache
#
# The variant images are built and pushed first. The base image is built
# last and gets each variant's reference pinned BY DIGEST (the variant
# index's digest, which resolves to the right platform when pulled) through
# the build argument <VARIANT>_IMAGE its Containerfile declares, so a base
# image always names exact variant bytes. A failure after the variants were
# pushed leaves their tags moved while the base image's tags are unchanged.
# That is harmless: the base image pins digests, so what it names does not
# change.
#
# The build is reproducible: SOURCE_DATE_EPOCH is the revision's commit time
# and file timestamps are rewritten to it, so the same files give the same
# digests. Provenance and SBOM attestations are not built in; the release
# workflow attaches its own.
#
# Static metadata (title, description, source, licenses) comes from the
# org.opencontainers.image.* LABEL lines of each Containerfile, written one
# label per line as LABEL key="value". It is repeated as annotations on the
# pushed index, where registries such as ghcr.io read it from. The revision,
# version and creation time are added as labels (and annotations) here.
#
# Needs bash, git and Docker with Buildx and a builder that can push
# multi-platform images (the docker-container driver, for example); the
# default `docker` driver cannot. Works with bash 3.2 (macOS).
set -euo pipefail

platforms='linux/amd64,linux/arm64'

usage() {
	sed -n '2,/^set -euo/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//' >&2
	exit 2
}

die() {
	echo "build-image.sh: $*" >&2
	exit 1
}

prefix=''
image_dir=''
tags=()
revision=''
version=''
digests_file=''
insecure=0
no_cache=0

while [ $# -gt 0 ]; do
	case "$1" in
	--prefix | --image-dir | --tag | --revision | --version | --digests-file)
		[ $# -ge 2 ] || die "$1 needs a value"
		case "$1" in
		--prefix) prefix="$2" ;;
		--image-dir) image_dir="$2" ;;
		--tag) tags+=("$2") ;;
		--revision) revision="$2" ;;
		--version) version="$2" ;;
		--digests-file) digests_file="$2" ;;
		esac
		shift 2
		;;
	--insecure)
		insecure=1
		shift
		;;
	--no-cache)
		no_cache=1
		shift
		;;
	-h | --help) usage ;;
	*) die "unknown argument: $1" ;;
	esac
done

[ -n "${prefix}" ] || die "--prefix is required"
[ -n "${image_dir}" ] || die "--image-dir is required"
[ "${#tags[@]}" -gt 0 ] || die "at least one --tag is required"
for t in "${tags[@]}"; do
	case "${t}" in
	'' | -* | .* | *[!A-Za-z0-9_.-]*) die "invalid --tag: ${t}" ;;
	esac
done
[ -f "${image_dir}/Containerfile" ] || die "${image_dir}/Containerfile not found"

image_dir="$(cd "${image_dir}" && pwd)"
base_name="$(basename "${image_dir}")"

docker_cmd="${DOCKER:-docker}"

# Reproducible timestamps: the revision's commit time unless the caller set
# SOURCE_DATE_EPOCH.
if [ -z "${SOURCE_DATE_EPOCH:-}" ]; then
	SOURCE_DATE_EPOCH="$(git -C "${image_dir}" log -1 --format=%ct "${revision:-HEAD}")" ||
		die "cannot read the commit time of ${revision:-HEAD}"
fi
export SOURCE_DATE_EPOCH
created="$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null ||
	date -u -r "${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)"

# label_value <Containerfile> <key> prints the value of the first
# LABEL key="value" line for key, and fails with a message when there is
# none or the line is not written in exactly that form.
label_value() {
	local key value
	key="$(printf '%s' "$2" | sed 's/\./\\./g')"
	value="$(sed -n "s/^LABEL[[:space:]][[:space:]]*${key}=\"\(.*\)\"[[:space:]]*\$/\1/p" "$1" | head -n 1)"
	if [ -z "${value}" ]; then
		echo "build-image.sh: $1: no non-empty line of the form LABEL $2=\"value\"" >&2
		return 1
	fi
	printf '%s' "${value}"
}

names_for() {
	local out='' t
	for t in "${tags[@]}"; do
		out="${out:+${out},}$1:${t}"
	done
	printf '%s' "${out}"
}

# build_image <repository> <Containerfile> <context> [build args...]
# builds and pushes one image under every tag, prints its digest.
build_image() {
	local repo="$1" containerfile="$2" context="$3"
	shift 3
	local args=(--platform "${platforms}" --file "${containerfile}" --provenance=false --sbom=false)
	local arg key value meta digest out
	for arg in "$@"; do
		args+=(--build-arg "${arg}")
	done

	args+=(--label "org.opencontainers.image.created=${created}")
	[ -z "${revision}" ] || args+=(--label "org.opencontainers.image.revision=${revision}")
	[ -z "${version}" ] || args+=(--label "org.opencontainers.image.version=${version}")
	args+=(--annotation "index:org.opencontainers.image.created=${created}")
	[ -z "${revision}" ] || args+=(--annotation "index:org.opencontainers.image.revision=${revision}")
	[ -z "${version}" ] || args+=(--annotation "index:org.opencontainers.image.version=${version}")
	for key in title description source licenses; do
		value="$(label_value "${containerfile}" "org.opencontainers.image.${key}")" || exit 1
		args+=(--annotation "index:org.opencontainers.image.${key}=${value}")
	done

	# The image names are one quoted CSV field inside --output.
	out="type=image,\"name=$(names_for "${repo}")\",push=true,oci-mediatypes=true,rewrite-timestamp=true"
	[ "${insecure}" -eq 0 ] || out="${out},registry.insecure=true"
	[ "${no_cache}" -eq 0 ] || args+=(--no-cache)

	meta="$(mktemp "${TMPDIR:-/tmp}/build-image.XXXXXX")"
	if ! "${docker_cmd}" buildx build "${args[@]}" --output "${out}" --metadata-file "${meta}" "${context}" >&2; then
		rm -f "${meta}"
		die "building ${repo} failed"
	fi
	digest="$(sed -n 's/.*"containerimage\.digest": *"\([^"]*\)".*/\1/p' "${meta}" | head -n 1)"
	rm -f "${meta}"
	[ -n "${digest}" ] || die "no image digest in the build metadata of ${repo}"

	if [ -n "${digests_file}" ]; then
		printf '%s %s\n' "${repo}" "${digest}" >>"${digests_file}"
	fi
	local t
	for t in "${tags[@]}"; do
		echo "pushed ${repo}:${t}@${digest}" >&2
	done
	printf '%s\n' "${digest}"
}

# Variants first, in name order.
base_args=()
for sub in "${image_dir}"/*/; do
	[ -f "${sub}Containerfile" ] || continue
	variant="$(basename "${sub}")"
	repo="${prefix}/${base_name}-${variant}"
	digest="$(build_image "${repo}" "${sub}Containerfile" "${sub%/}")"
	var="$(printf '%s' "${variant}" | tr 'a-z-' 'A-Z_')"
	base_args+=("${var}_IMAGE=${repo}@${digest}")
done

build_image "${prefix}/${base_name}" "${image_dir}/Containerfile" "${image_dir}" \
	${base_args[@]+"${base_args[@]}"} >/dev/null
