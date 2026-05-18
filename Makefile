# Root Makefile — thin wrapper that delegates to the focused makefiles
# under example/, web/, and test/smoke/. Run `make help` for the list.

.PHONY: help build smoke smoke-all smoke-clean

help:
	@echo "Targets:"
	@echo "  build       — go build ./..."
	@echo "  smoke       — render chippy VHS smoke tapes (test/smoke/*.tape)"
	@echo "  smoke-all   — render every tape (incl. nessy-attach)"
	@echo "  smoke-clean — wipe test/smoke/out/"

build:
	go build ./...

# Smoke tapes need the binaries that the tapes call. We rebuild them
# every invocation so the rendered GIFs always match the current tree.
smoke: chippy
	$(MAKE) -C test/smoke

smoke-all: chippy nessy
	$(MAKE) -C test/smoke all

smoke-clean:
	$(MAKE) -C test/smoke clean

.PHONY: chippy nessy

chippy:
	go build -o chippy ./cmd/chippy

nessy:
	go build -tags=nessy -o nessy ./cmd/nessy
