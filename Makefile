# Root Makefile — thin wrapper that delegates to the focused makefiles
# under example/, web/, and test/smoke/. Run `make help` for the list.

.PHONY: help build smoke smoke-clean chippy

help:
	@echo "Targets:"
	@echo "  build       — go build ./..."
	@echo "  smoke       — render chippy VHS smoke tapes (test/smoke/*.tape)"
	@echo "  smoke-clean — wipe test/smoke/out/"

build:
	go build ./...

# Smoke tapes need the chippy binary the tapes call. Rebuild every
# invocation so the rendered GIFs always match the current tree.
smoke: chippy
	$(MAKE) -C test/smoke

smoke-clean:
	$(MAKE) -C test/smoke clean

chippy:
	go build -o chippy ./cmd/chippy
