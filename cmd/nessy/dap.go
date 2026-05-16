//go:build nessy

package main

import (
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/nkane/chippy/internal/dap"
)

// dapAttached counts active DAP sessions. The game loop checks this
// before stepping the CPU — when one or more clients are attached,
// the server's `continue` runLoop owns execution and the game loop
// only paints frames. nil → no client → autonomous run.
//
// Package-level since there is one game per process. The counter is
// atomic because OnAttached / OnDisconnected fire from the DAP
// session goroutine while the game loop reads from the Ebiten Update
// goroutine.
var dapAttached atomic.Int32

// runDAPListener accepts incoming DAP connections on the given TCP port
// and serves each one against the live NES bus. The cpuMu is shared
// with the Ebiten game loop so concurrent requests never observe a
// mid-instruction register snapshot. Same pattern chippy uses behind
// `:dap PORT` (internal/tui/dap_attach.go) — see issue #97 for design
// notes.
//
// OnAttached / OnDisconnected callbacks increment / decrement
// `dapAttached`. The game loop checks that counter to decide whether
// to step the CPU. When a client attaches, the server's runLoop (set
// up by `continue`) becomes the sole stepper; the game loop yields.
// When the last client disconnects, the game loop resumes its
// autonomous run.
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
				OnAttached: func() {
					dapAttached.Add(1)
				},
				OnDisconnected: func() {
					dapAttached.Add(-1)
				},
			}
			if err := s.AttachExisting(cfg); err != nil {
				fmt.Fprintln(os.Stderr, "nessy: DAP attach:", err)
				return
			}
			_ = s.Serve()
		}(conn)
	}
}
