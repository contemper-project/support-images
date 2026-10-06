#!/bin/sh
# Runs shellcheck on every shell or OpenRC script under hack/ and images/,
# found by its shebang. Scripts with a shebang shellcheck knows are checked
# as that shell; OpenRC scripts (#!/sbin/openrc-run) as POSIX sh.
set -eu

cd "$(dirname "$0")/.."
status=0
scripts="$(grep -rIl -e '^#!.*\(sh\|openrc-run\)$' hack images 2>/dev/null || true)"
while IFS= read -r f; do
	[ -n "${f}" ] || continue
	case "$(head -n 1 "${f}")" in
	*openrc-run) shellcheck -s sh "${f}" || status=1 ;;
	*) shellcheck "${f}" || status=1 ;;
	esac
done <<EOF2
${scripts}
EOF2
exit "${status}"
