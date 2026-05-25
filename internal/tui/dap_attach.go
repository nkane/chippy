package tui

import (
	"fmt"
	"net"
	"sync"

	"github.com/nkane/chippy/dap"
)

// cmdDAP implements `:dap PORT` — spawn a DAP server in attach mode
// against the TUI's existing CPU/RAM/MMIO. The server runs on its own
// goroutine; AttachConfig.CPUMu serializes its access with the TUI.
//
//	:dap         — report current listener (or "no DAP attached")
//	:dap PORT    — start listening on tcp:PORT
//	:dap 0       — start on a free port (auto-assigned)
//	:dap stop    — close the listener (sessions in flight are dropped)
//
// Only one listener at a time; second `:dap PORT` while one is live
// reports an error. The session lifecycle is the editor's job after
// that — chippy doesn't track individual TCP connections.
func (m *Model) cmdDAP(args []string) string {
	if len(args) == 0 {
		if m.DAPListenAddr == "" {
			return "dap: no listener (try `:dap 14785`)"
		}
		return fmt.Sprintf("dap: listening on %s", m.DAPListenAddr)
	}
	if args[0] == "stop" {
		if dapListener == nil {
			return "dap: no listener to stop"
		}
		_ = dapListener.Close()
		dapListener = nil
		m.DAPListenAddr = ""
		return "dap: listener stopped"
	}
	if dapListener != nil {
		return fmt.Sprintf("dap: already listening on %s (try `:dap stop` first)", m.DAPListenAddr)
	}
	addr := ":" + args[0]
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Sprintf("dap: listen %s: %v", addr, err)
	}
	dapListener = ln
	if m.CPUMu == nil {
		m.CPUMu = &sync.Mutex{}
	}
	m.DAPListenAddr = ln.Addr().String()

	// Accept connections in a goroutine; each accepted conn drives one
	// DAP session via Server.AttachExisting + Serve. AcceptError on
	// closed listener is the normal shutdown path.
	cfg := dap.AttachConfig{
		CPU:     m.CPU,
		RAM:     m.RAM,
		Tracer:  m.Tracer,
		Syms:    m.Syms,
		SrcMap:  m.SrcMap,
		TextOut: m.TextOut,
		KeyIn:   m.Keyboard,
		CPUMu:   m.CPUMu,
	}
	go acceptDAPSessions(ln, cfg)
	return fmt.Sprintf("dap: listening on %s", m.DAPListenAddr)
}

// dapListener is a package-level singleton because chippy supports
// only one editor connection at a time. Lifetime: Listen() in
// cmdDAP → close on `:dap stop` or process exit. The TUI Model
// references it via the DAPListenAddr string snapshot.
var dapListener net.Listener

func acceptDAPSessions(ln net.Listener, cfg dap.AttachConfig) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed; exit silently
		}
		go func(c net.Conn) {
			defer func() { _ = c.Close() }()
			s := dap.NewServer(c, c)
			if err := s.AttachExisting(cfg); err != nil {
				return
			}
			_ = s.Serve()
		}(conn)
	}
}
