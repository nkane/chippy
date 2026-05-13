#!/usr/bin/env bash
# record-onboarding.sh — produce docs/media/onboarding.cast +
# onboarding.gif for the README's hero animation.
#
# Requirements:
#   asciinema    — record terminal sessions
#   agg          — render .cast → .gif (https://github.com/asciinema/agg)
#   chippy       — `go install ./cmd/chippy` or use a release build
#   example/c    — pre-built (cd example/c && make all)
#
# Usage:
#   docs/media/record-onboarding.sh
#
# The script drives a scripted ~25-second session in a child terminal
# (via tmux send-keys / `expect`). To re-record, just run again — the
# cast + gif are overwritten in place.

set -euo pipefail

cd "$(dirname "$0")/../.."

if ! command -v asciinema >/dev/null; then
  echo "install asciinema: brew install asciinema (or apt install asciinema)" >&2
  exit 1
fi
if ! command -v agg >/dev/null; then
  echo "install agg: cargo install --git https://github.com/asciinema/agg" >&2
  exit 1
fi

# Build a fresh chippy + the C examples if missing.
go build -o /tmp/chippy ./cmd/chippy
make -C example/c hello.bin >/dev/null

CAST=docs/media/onboarding.cast
GIF=docs/media/onboarding.gif

# Drive a scripted session. The `expect`-flavored bits below are
# bash-friendly: sleep between input lines so each frame is readable
# in the resulting GIF.
SCRIPT=$(mktemp)
cat >"$SCRIPT" <<'EOS'
#!/usr/bin/env bash
set -e
/tmp/chippy -rom example/c/hello.bin &
PID=$!
# Give the TUI time to render.
sleep 1.5
# Keystrokes are sent into the TTY via tmux below — this script just
# launches chippy and waits to be killed by the parent.
wait $PID 2>/dev/null || true
EOS
chmod +x "$SCRIPT"

# 25-second cap on the recording; that's roughly the longest you want
# a hero GIF to be before it loops.
asciinema rec --overwrite --command "$SCRIPT" --idle-time-limit 1 --title "chippy — your first ROM in 25 s" "$CAST"

# Render with a 30 cols-wide / 10 lines tall window — README hero
# rendering looks crisper than the default 80x24.
agg --theme monokai --speed 1.5 --cols 110 --rows 32 "$CAST" "$GIF"

echo "wrote $CAST and $GIF"
