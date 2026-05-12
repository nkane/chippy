.PHONY: all wasm serve clean

# Build target lives next to the HTML so the page can fetch it relative
# to itself. wasm_exec.js is also shipped from this directory.
WASM := chippy.wasm

all: $(WASM)

$(WASM):
	cd .. && GOOS=js GOARCH=wasm go build -o web/$(WASM) ./cmd/chippy-wasm

# Refresh the runtime shim from the active Go toolchain.
wasm_exec.js:
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" .

# Quick local server. Any static-file server works; we use Python because
# it ships everywhere a Go dev's machine already has Go (and python3).
serve: all
	@echo "serving http://localhost:8080/"
	@python3 -m http.server 8080

clean:
	rm -f $(WASM)
