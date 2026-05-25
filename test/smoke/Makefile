# test/smoke/Makefile — drives VHS-based TUI smoke tests.
#
# Renders one .gif per tape into test/smoke/out/. Reviewers scrub the
# GIFs in PRs; CI just checks vhs exited 0 (i.e. the tape didn't crash
# mid-render).
#
# Targets:
#   make              → run every chippy tape (no nessy build dependency)
#   make all          → every tape + nessy-record captures
#   make chippy       → run only chippy-* tapes
#   make nessy        → run only nessy-* tapes (requires ./nessy built)
#   make nessy-record → headless GIF/MP4 captures of demo ROMs,
#                       synthesized from the emulator (no GL, no window,
#                       deterministic). MP4 needs ffmpeg.
#   make clean        → remove rendered output
#
# This Makefile is invoked from the repo root via `make smoke`. Run
# from the repo root, not from this directory — every tape uses
# repo-root-relative paths (./chippy, example/, etc.).

REPO_ROOT := $(abspath $(dir $(firstword $(MAKEFILE_LIST)))../..)
OUT       := $(REPO_ROOT)/test/smoke/out
CHIPPY    := $(REPO_ROOT)/chippy
NESSY     := $(REPO_ROOT)/nessy
NREC      := $(REPO_ROOT)/nessy-record

CHIPPY_TAPES := \
	$(REPO_ROOT)/test/smoke/chippy-source-scroll.tape \
	$(REPO_ROOT)/test/smoke/chippy-syms.tape \
	$(REPO_ROOT)/test/smoke/chippy-bp-and-run.tape \
	$(REPO_ROOT)/test/smoke/chippy-console.tape

NESSY_TAPES := \
	$(REPO_ROOT)/test/smoke/nessy-attach.tape

.PHONY: default all chippy nessy nessy-record clean check-chippy check-nessy

default: chippy

all: chippy nessy nessy-record

chippy: check-chippy $(OUT)
	@for tape in $(CHIPPY_TAPES); do \
		echo ">>> $$tape"; \
		(cd $(REPO_ROOT) && vhs $$tape) || exit 1; \
	done

nessy: check-chippy check-nessy $(OUT)
	@for tape in $(NESSY_TAPES); do \
		echo ">>> $$tape"; \
		(cd $(REPO_ROOT) && vhs $$tape) || exit 1; \
	done

# nessy-record: headless captures synthesized straight from the
# emulator — no Ebiten window, no OpenGL, no screen grab. Builds the
# untagged recorder (no GL deps) and renders a handful of demos:
# a visual GIF, a sprite-stress GIF, an input-scripted GIF, and an
# audio MP4 (video + sound). Deterministic, so the artifacts are
# stable across runs.
nessy-record: $(OUT)
	cd $(REPO_ROOT) && go build -o $(NREC) ./cmd/nessy-record
	cd $(REPO_ROOT) && $(NREC) -rom roms/demos/vblank-bounce/vblank-bounce.nes -frames 120 -o $(OUT)/nessy-vblank-bounce.gif
	cd $(REPO_ROOT) && $(NREC) -rom roms/demos/oam-grid/oam-grid.nes -frames 60 -o $(OUT)/nessy-oam-grid.gif
	cd $(REPO_ROOT) && $(NREC) -rom roms/demos/input-echo/input-echo.nes -script test/smoke/nessy-input-echo.json -frames 120 -o $(OUT)/nessy-input-echo.gif
	cd $(REPO_ROOT) && $(NREC) -rom roms/demos/all-channels/all-channels.nes -frames 120 -o $(OUT)/nessy-all-channels.mp4

$(OUT):
	mkdir -p $(OUT)

check-chippy:
	@test -x $(CHIPPY) || { echo "missing $(CHIPPY) — run 'go build -o chippy ./cmd/chippy' from $(REPO_ROOT)"; exit 1; }

check-nessy:
	@test -x $(NESSY) || { echo "missing $(NESSY) — run 'go build -tags=nessy -o nessy ./cmd/nessy' from $(REPO_ROOT)"; exit 1; }

clean:
	rm -rf $(OUT)
