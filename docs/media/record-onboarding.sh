#!/usr/bin/env bash
# record-onboarding.sh — render docs/media/onboarding.gif via vhs.
#
# Why vhs (and not asciinema + agg): Bubble Tea uses altscreen mode,
# which asciinema records as a sequence of ANSI control codes that
# agg renders 1:1. Result: garbled or empty playback. vhs
# (https://github.com/charmbracelet/vhs) was written by the same
# people who wrote Bubble Tea — it drives the PTY directly with
# scripted keystrokes from a declarative .tape file and produces a
# clean GIF.
#
# Requirements: vhs, chippy on PATH, example/c/hello.bin built
#   brew install vhs            (or: go install github.com/charmbracelet/vhs@latest)
#   go install ./cmd/chippy
#   make -C example/c hello.bin
#
# Usage:
#   docs/media/record-onboarding.sh

set -euo pipefail
cd "$(dirname "$0")/../.."

for tool in vhs chippy; do
  if ! command -v "$tool" >/dev/null; then
    echo "missing dependency: $tool" >&2
    case $tool in
      vhs)    echo "  brew install vhs   (or: go install github.com/charmbracelet/vhs@latest)" >&2 ;;
      chippy) echo "  go install ./cmd/chippy" >&2 ;;
    esac
    exit 1
  fi
done

if [ ! -f example/c/hello.bin ]; then
  echo "example/c/hello.bin missing — build it first:" >&2
  echo "  make -C example/c hello.bin" >&2
  exit 1
fi

vhs docs/media/onboarding.tape

echo "wrote docs/media/onboarding.gif"
echo "uncomment the screencast line in README.md to surface it."
