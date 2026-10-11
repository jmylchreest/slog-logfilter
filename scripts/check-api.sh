#!/usr/bin/env bash
# Run with a current stable Go toolchain. The tool is a development dependency
# only; it does not add requirements to this library's go.mod or go.sum.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
baseline=${1:-$(git describe --tags --match 'v[0-9]*' --abbrev=0 HEAD^)}
if [[ ! "$baseline" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Expected a stable version tag, got: $baseline" >&2
    exit 1
fi
git rev-parse --verify "refs/tags/$baseline^{commit}" >/dev/null

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
mkdir "$scratch/baseline" "$scratch/bin"
git archive "refs/tags/$baseline" | tar -xf - -C "$scratch/baseline"

# Pin tooling independently of the library's minimum Go version.
GOBIN="$scratch/bin" go install golang.org/x/exp/cmd/apidiff@v0.0.0-20261011100332-faecfb24eb49
apidiff="$scratch/bin/apidiff"
module=$(go list -m)
(
    cd "$scratch/baseline"
    "$apidiff" -m -w "$scratch/baseline.api" "$module"
)
"$apidiff" -m -w "$scratch/current.api" "$module"
"$apidiff" -m -incompatible "$scratch/baseline.api" "$scratch/current.api" > "$scratch/report"

# apidiff reports incompatibilities on stdout but exits successfully.
if [[ -s "$scratch/report" ]]; then
    approved="scripts/api-breaks/$baseline.txt"
    if [[ ! -f "$approved" ]] || ! diff -u "$approved" "$scratch/report"; then
        cat "$scratch/report" >&2
        exit 1
    fi
    echo "Only approved API breaks against $baseline; see CHANGELOG.md for migration."
    exit 0
fi
echo "Exported Go API is source-compatible with $baseline; check behavioural migration notes separately."
