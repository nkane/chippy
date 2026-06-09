package main

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/dap"
)

// runDAP starts a Debug Adapter Protocol server in the requested transport
// mode and blocks until the session ends. Supported modes:
//
//	stdio       — read requests from os.Stdin, write responses to os.Stdout.
//	             VS Code default; editor spawns chippy and pipes.
//	tcp:PORT    — listen on TCP, accept exactly one connection. nvim-dap
//	             default; editor connects out.
//	unix:PATH   — listen on a unix-domain socket, accept one connection.
//	             Lowest-overhead out-of-process local transport (nvim-dap /
//	             vscode-chippy on the same host).
//	inproc      — in-process loopback: a server + in-process client (no
//	             sockets, no JSON framing) running a short self-check. The
//	             transport (dap.NewInprocServer) is the foundation for the
//	             embedded TUI-via-DAP build (#394).
//
// On exit, normal CLI errors print to stderr. The dap package itself logs
// to stderr too — never stdout, which is the protocol channel.
func runDAP(mode string) {
	switch {
	case mode == "stdio":
		serveConn(dap.NewServer(os.Stdin, os.Stdout))
	case strings.HasPrefix(mode, "tcp:"):
		serveConn(acceptOne("tcp", ":"+mode[len("tcp:"):]))
	case strings.HasPrefix(mode, "unix:"):
		serveConn(acceptOne("unix", mode[len("unix:"):]))
	case mode == "inproc":
		runInprocLoopback()
	default:
		fmt.Fprintf(os.Stderr, "unknown -dap mode %q (want stdio, tcp:PORT, unix:PATH, or inproc)\n", mode)
		os.Exit(2)
	}
}

// acceptOne listens on the given network/address, accepts exactly one
// connection, and returns a Server bound to it.
func acceptOne(network, addr string) *dap.Server {
	if network == "unix" {
		_ = os.Remove(addr) // clear a stale socket from a prior crash
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dap: listen:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "chippy dap: listening on %s %s\n", network, addr)
	conn, err := ln.Accept()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dap: accept:", err)
		os.Exit(1)
	}
	_ = ln.Close()
	return dap.NewServer(conn, conn)
}

func serveConn(srv *dap.Server) {
	if err := srv.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, "dap:", err)
		os.Exit(1)
	}
}

// runInprocLoopback exercises the in-process transport end to end so
// `-dap inproc` has something to do standalone. The real consumer is an
// in-process TUI client (a future build); here we just prove the round trip.
func runInprocLoopback() {
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	srv, cl := dap.NewInprocServer()
	if err := srv.AttachExisting(dap.AttachConfig{CPU: c, RAM: ram}); err != nil {
		fmt.Fprintln(os.Stderr, "dap: inproc attach:", err)
		os.Exit(1)
	}
	if _, err := cl.Initialize(); err != nil {
		fmt.Fprintln(os.Stderr, "dap: inproc initialize:", err)
		os.Exit(1)
	}
	if _, err := cl.Attach(); err != nil {
		fmt.Fprintln(os.Stderr, "dap: inproc attach req:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "chippy dap: in-process transport OK (server + client round-trip)")
}
