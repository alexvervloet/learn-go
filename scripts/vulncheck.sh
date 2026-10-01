#!/usr/bin/env bash
# Run govulncheck in every workspace module and fail on any vulnerability the code calls, except the ones
# listed in .govulncheck-allow.
#
# govulncheck has no ignore flag, so this reads its JSON. A finding whose trace names a function is one the
# code can reach; the module- and package-level findings are reported as information, as govulncheck itself does.
#
# Usage: scripts/vulncheck.sh <govulncheck version>
set -euo pipefail

version="${1:?usage: scripts/vulncheck.sh <govulncheck version>}"
root="$(cd "$(dirname "$0")/.." && pwd)"
allowed="$(grep -v '^#' "$root/.govulncheck-allow" | awk 'NF { print $1 }')"

status=0

for mod in $(go list -m -f '{{.Dir}}'); do
	# GOWORK=off so each module is checked against its own go.mod, which is what a user of it would build.
	found="$(cd "$mod" && GOWORK=off go run "golang.org/x/vuln/cmd/govulncheck@$version" -format json ./... |
		jq -r 'select(.finding and .finding.trace[0].function) | .finding.osv' | sort -u)"

	for id in $found; do
		if grep -qx "$id" <<<"$allowed"; then
			echo "${mod#"$root"/}: $id (allowed, see .govulncheck-allow)"
		else
			echo "${mod#"$root"/}: $id  https://pkg.go.dev/vuln/$id"
			status=1
		fi
	done
done

if [[ $status -ne 0 ]]; then
	echo "for the call traces: cd <module> && GOWORK=off go run golang.org/x/vuln/cmd/govulncheck@$version ./..." >&2
fi

exit $status
