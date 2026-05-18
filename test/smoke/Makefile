# test/smoke/Makefile — drives VHS-based TUI smoke tests.
#
# Renders one .gif per tape into test/smoke/out/. Reviewers scrub the
# GIFs in PRs; CI just checks vhs exited 0 (i.e. the tape didn't crash
# mid-render).
#
# Targets:
#   make          → run every chippy tape (no nessy build dependency)
#   make all      → run every tape (incl. nessy-attach)
#   make chippy   → run only chippy-* tapes
#   make nessy    → run only nessy-* tapes (requires ./nessy built)
#   make clean    → remove rendered output
#
# This Makefile is invoked from the repo root via `make smoke`. Run
# from the repo root, not from this directory — every tape uses
# repo-root-relative paths (./chippy, example/, etc.).

REPO_ROOT := $(abspath $(dir $(firstword $(MAKEFILE_LIST)))../..)
OUT       := $(REPO_ROOT)/test/smoke/out
CHIPPY    := $(REPO_ROOT)/chippy
NESSY     := $(REPO_ROOT)/nessy

CHIPPY_TAPES := \
	$(REPO_ROOT)/test/smoke/chippy-source-scroll.tape \
	$(REPO_ROOT)/test/smoke/chippy-syms.tape \
	$(REPO_ROOT)/test/smoke/chippy-bp-and-run.tape

NESSY_TAPES := \
	$(REPO_ROOT)/test/smoke/nessy-attach.tape

.PHONY: default all chippy nessy clean check-chippy check-nessy

default: chippy

all: chippy nessy

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

$(OUT):
	mkdir -p $(OUT)

check-chippy:
	@test -x $(CHIPPY) || { echo "missing $(CHIPPY) — run 'go build -o chippy ./cmd/chippy' from $(REPO_ROOT)"; exit 1; }

check-nessy:
	@test -x $(NESSY) || { echo "missing $(NESSY) — run 'go build -tags=nessy -o nessy ./cmd/nessy' from $(REPO_ROOT)"; exit 1; }

clean:
	rm -rf $(OUT)
