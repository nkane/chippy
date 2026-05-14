//go:build !nessy

package main

import (
	"fmt"
	"os"
)

// Default build of `nessy` is a stub: the real binary pulls in Ebiten,
// which requires X11 / GL dev headers on Linux that chippy's default CI
// containers don't carry. Build with `-tags=nessy` to get the actual
// emulator:
//
//	go build -tags=nessy -o nessy ./cmd/nessy
//	./nessy game.nes
//
// On darwin / windows the deps are bundled with the OS, so the tag is
// the only extra step. On Linux you'll need:
//
//	sudo apt-get install -y libgl1-mesa-dev xorg-dev libasound2-dev \
//	  libxcursor-dev libxinerama-dev libxi-dev libxrandr-dev
func main() {
	fmt.Fprintln(os.Stderr, "nessy: build with -tags=nessy to enable the Ebiten game loop")
	fmt.Fprintln(os.Stderr, "  go build -tags=nessy -o nessy ./cmd/nessy")
	os.Exit(2)
}
