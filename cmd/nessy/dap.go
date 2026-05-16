//go:build nessy

package main

import (
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"

	"github.com/nkane/chippy/internal/dap"
	"github.com/nkane/chippy/internal/symbols"
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

// waitForAttach blocks the game loop's CPU stepping at boot when
// nessy was launched under `chippy -nessy …`. Set by main from the
// -wait-for-debugger flag BEFORE the DAP listener starts so a fast
// client attach can't lose the race. Cleared the first time a DAP
// client actually attaches (OnAttached fires).
//
// Note this is a one-shot gate: once cleared we don't re-arm on
// disconnect. The user has confirmed they're driving the debugger;
// closing the TUI shouldn't suddenly restart the game.
var waitForAttach atomic.Bool

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
func runDAPListener(port int, bus *nesBus, cpuMu *sync.Mutex, syms *symbols.Table, srcMap *symbols.SourceMap) {
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
				CPU:    bus.cpu,
				RAM:    bus.ram,
				MMIO:   bus.mmio,
				CPUMu:  cpuMu,
				Syms:   syms,
				SrcMap: srcMap,
				OnAttached: func() {
					// Order matters. The game loop checks
					// `waitForAttach || dapAttached>0`. If we
					// cleared waitForAttach first and the loop
					// raced through Update before dapAttached
					// incremented, both gates would briefly read
					// "off" — the loop would fall through, block
					// on cpuMu (held by the dispatch handling
					// this very attach), and on cpuMu release
					// step ~30k cycles right past reset.
					//
					// Increment dapAttached FIRST so at least one
					// gate is always "on" during the transition,
					// then drop the boot wait.
					dapAttached.Add(1)
					waitForAttach.Store(false)
					fmt.Fprintln(os.Stderr, "nessy: DAP client attached — debugger has control of CPU execution")
				},
				OnDisconnected: func() {
					// Clamp at zero. The dap.Server promises to
					// pair OnAttached with OnDisconnected, but we
					// keep the floor as defensive depth — a stray
					// disconnect for an un-attached session would
					// otherwise drop the gate even though the
					// real session is still running.
					for {
						cur := dapAttached.Load()
						if cur <= 0 {
							return
						}
						if dapAttached.CompareAndSwap(cur, cur-1) {
							return
						}
					}
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
