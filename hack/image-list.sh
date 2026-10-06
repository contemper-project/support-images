#!/bin/sh
# Prints, as a JSON array on one line, every image the given image
# directories build: for each directory the base image and every variant
# image its image.json names (those with a dir), as
# {"component": "<dir name>", "image": "<image name>"}. The release and
# latest workflows use it to attest each pushed image.
#
# Usage: hack/image-list.sh images/<name>...
# Needs jq.
set -eu

list='[]'
for dir in "$@"; do
	name="$(basename "${dir}")"
	list="$(printf '%s' "${list}" | jq -c --arg name "${name}" --slurpfile spec "${dir}/image.json" '
		. + [ {component: $name, image: $name} ]
			+ [ ($spec[0].branches // [])[] | (.variants // [])[]
				| select((.dir // "") != "")
				| {component: $name, image: ($name + .image_suffix)} ]')"
done
printf '%s\n' "${list}"
