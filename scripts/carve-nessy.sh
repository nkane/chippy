#!/usr/bin/env bash
# carve-nessy.sh — extract the nessy emulator into its own repo (#351).
#
# Run this AFTER:
#   1. github.com/nkane/nessy exists (empty).
#   2. chippy has a published library tag to pin (e.g. v1.1.0) — the
#      core packages cpu/dap/peripheral/expr/loader/symbols/trace are
#      already public (#349).
#
# It does NOT mutate the chippy repo. It produces a fresh nessy repo
# from a filtered clone of chippy history, keeping only nessy paths +
# rewriting the nessy-internal import paths to the new module.
#
# Requires: git-filter-repo (https://github.com/newren/git-filter-repo).
#
# Usage:
#   scripts/carve-nessy.sh <chippy-version-tag> <workdir>
#   e.g.  scripts/carve-nessy.sh v1.1.0 /tmp/nessy-carve
#
# After it finishes, review /tmp/nessy-carve/nessy then push it to
# github.com/nkane/nessy (the script prints the exact commands).

set -euo pipefail

CHIPPY_VERSION="${1:?usage: carve-nessy.sh <chippy-version-tag> <workdir>}"
WORKDIR="${2:?usage: carve-nessy.sh <chippy-version-tag> <workdir>}"
CHIPPY_REMOTE="git@github.com:nkane/chippy.git"
NESSY_MODULE="github.com/nkane/nessy"
CHIPPY_MODULE="github.com/nkane/chippy"

if ! command -v git-filter-repo >/dev/null 2>&1; then
  echo "error: git-filter-repo not found — install it first:" >&2
  echo "  brew install git-filter-repo   # or pip install git-filter-repo" >&2
  exit 1
fi

mkdir -p "$WORKDIR"
cd "$WORKDIR"
rm -rf nessy
git clone "$CHIPPY_REMOTE" nessy
cd nessy

# Paths that move to the nessy repo (everything nessy-specific). The
# shared 6502 core stays in chippy + is pulled in as a module dep.
git filter-repo \
  --path cmd/nessy/ \
  --path cmd/nessy-wasm/ \
  --path cmd/nessy-record/ \
  --path internal/nes/ \
  --path roms/demos/ \
  --path web/nessy/ \
  --path docs/nessy/ \
  --path test/smoke/nessy-input-echo.json \
  --path-glob 'roms/demos/*' \
  --tag-rename 'nessy-v:v'

# Rewrite the nessy-internal import paths to the new module. The core
# imports (github.com/nkane/chippy/cpu, /dap, …) are left untouched —
# they resolve through the go.mod require added below.
grep -rl "${CHIPPY_MODULE}/internal/nes" --include='*.go' . \
  | xargs -r sed -i.bak "s|${CHIPPY_MODULE}/internal/nes|${NESSY_MODULE}/internal/nes|g"
find . -name '*.bak' -delete

# Fresh go.mod for the nessy module, pinning the chippy core library.
cat > go.mod <<EOF
module ${NESSY_MODULE}

go 1.23

require (
	${CHIPPY_MODULE} ${CHIPPY_VERSION}
	github.com/hajimehoshi/ebiten/v2 v2.9.9
)
EOF

echo
echo "==> nessy repo staged in $WORKDIR/nessy"
echo "Next:"
echo "  cd $WORKDIR/nessy"
echo "  go mod tidy            # resolve ebiten + transitive deps"
echo "  go build -tags=nessy ./... && go test -race ./..."
echo "  git remote add origin git@github.com:nkane/nessy.git"
echo "  git push -u origin main --tags"
