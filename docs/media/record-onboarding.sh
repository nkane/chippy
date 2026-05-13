#!/usr/bin/env bash
# record-onboarding.sh — capture docs/media/onboarding.cast +
# onboarding.gif for the README hero.
#
# Wrapper around asciinema + agg. You drive the chippy session
# yourself during the recording (Bubble Tea's altscreen and
# scripted-keystroke shims don't cooperate cleanly — keys get
# dropped or mis-ordered).
#
# Suggested session (~25 seconds, hero-GIF-friendly):
#   chippy -rom example/c/hello.bin
#   wait ~2s for the TUI to settle
#   press `s` four times — watch the reg pane update
#   press `r` to free-run, then `r` again to pause
#   press `<` once to reverse-step
#   press `q` to quit
#
# Requirements: asciinema, agg, chippy on PATH, example/c/hello.bin
# built (cc65 needed; `make -C example/c hello.bin`).

set -euo pipefail
cd "$(dirname "$0")/../.."

for tool in asciinema agg chippy; do
  if ! command -v "$tool" >/dev/null; then
    echo "missing dependency: $tool" >&2
    case $tool in
      asciinema) echo "  brew install asciinema   (or apt install asciinema)" >&2 ;;
      agg)       echo "  cargo install --git https://github.com/asciinema/agg" >&2 ;;
      chippy)    echo "  go install ./cmd/chippy" >&2 ;;
    esac
    exit 1
  fi
done

if [ ! -f example/c/hello.bin ]; then
  echo "example/c/hello.bin missing — build it first:" >&2
  echo "  make -C example/c hello.bin" >&2
  exit 1
fi

CAST=docs/media/onboarding.cast
GIF=docs/media/onboarding.gif

cat <<EOF

Recording $CAST. Drive the session yourself:

  1. chippy -rom example/c/hello.bin
  2. wait ~2 seconds (let the TUI settle)
  3. press s s s s    (step four instructions)
  4. press r          (free-run)
  5. press r          (pause)
  6. press <          (reverse-step once)
  7. press q          (quit)

Aim for ~25 seconds total. Ctrl-D when done.

EOF

asciinema rec --overwrite --idle-time-limit 1 \
  --title "chippy — your first ROM in 25 s" \
  --cols 110 --rows 32 \
  "$CAST"

echo "rendering $GIF..."
agg --theme monokai --speed 1.5 --cols 110 --rows 32 "$CAST" "$GIF"

echo "wrote $CAST and $GIF"
echo "uncomment the screencast line in README.md to surface it."
