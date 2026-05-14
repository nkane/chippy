//go:build nessy

package main

import (
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/nkane/chippy/internal/dap"
)

// runDAPListener accepts incoming DAP connections on the given TCP port
// and serves each one against the live NES bus. The cpuMu is shared
// with the Ebiten game loop so concurrent requests never observe a
// mid-instruction register snapshot. Same pattern chippy uses behind
// `:dap PORT` (internal/tui/dap_attach.go) — see issue #97 for design
// notes.
//
// Currently the DAP server's own continue loop and nessy's game loop
// will both call cpu.Step() under the same mutex once a client issues
// `continue`. The mutex serializes them but they double-step the CPU.
// For v0.1 this is acceptable (TUI `:dap` path has the same property);
// real "pause" semantics that gate the game loop on a remote flag is a
// v0.2 polish item.
func runDAPListener(port int, bus *nesBus, cpuMu *sync.Mutex) {
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nessy: DAP listen:", err)
		return
	}
	defer func() { _ = ln.Close() }()

	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed; exit silently
		}
		go func(c net.Conn) {
			defer func() { _ = c.Close() }()
			s := dap.NewServer(c, c)
			cfg := dap.AttachConfig{
				CPU:   bus.cpu,
				RAM:   bus.ram,
				MMIO:  bus.mmio,
				CPUMu: cpuMu,
			}
			if err := s.AttachExisting(cfg); err != nil {
				fmt.Fprintln(os.Stderr, "nessy: DAP attach:", err)
				return
			}
			_ = s.Serve()
		}(conn)
	}
}
