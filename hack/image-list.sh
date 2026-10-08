#!/bin/sh
# Prints, as a JSON array on one line, every image the given image
# directories build: for each directory the base image and every variant
# image, as {"component": "<dir name>", "image": "<image name>"}. The
# layout decides: images/<name>/Containerfile is the image <name>, and
# every subdirectory images/<name>/<variant>/ that holds a Containerfile is
# the image <name>-<variant>. The release and latest workflows use it to
# attest each pushed image.
#
# Usage: hack/image-list.sh images/<name>...
set -eu

sep=''
printf '['
for dir in "$@"; do
	dir="${dir%/}"
	name="$(basename "${dir}")"
	printf '%s{"component":"%s","image":"%s"}' "${sep}" "${name}" "${name}"
	sep=','
	for sub in "${dir}"/*/; do
		[ -f "${sub}Containerfile" ] || continue
		variant="$(basename "${sub}")"
		printf ',{"component":"%s","image":"%s-%s"}' "${name}" "${name}" "${variant}"
	done
done
printf ']\n'
