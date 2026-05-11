package main

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/nkane/chippy/internal/dap"
)

// runDAP starts a Debug Adapter Protocol server in the requested transport
// mode and blocks until the session ends. Supported modes:
//
//	stdio       — read requests from os.Stdin, write responses to os.Stdout.
//	             VS Code default; editor spawns chippy and pipes.
//	tcp:PORT    — listen on TCP, accept exactly one connection. nvim-dap
//	             default; editor connects out.
//
// On exit, normal CLI errors print to stderr. The dap package itself logs
// to stderr too — never stdout, which is the protocol channel.
func runDAP(mode string) {
	switch {
	case mode == "stdio":
		srv := dap.NewServer(os.Stdin, os.Stdout)
		if err := srv.Serve(); err != nil {
			fmt.Fprintln(os.Stderr, "dap:", err)
			os.Exit(1)
		}
	case strings.HasPrefix(mode, "tcp:"):
		port := mode[len("tcp:"):]
		ln, err := net.Listen("tcp", ":"+port)
		if err != nil {
			fmt.Fprintln(os.Stderr, "dap: listen:", err)
			os.Exit(1)
		}
		defer func() { _ = ln.Close() }()
		fmt.Fprintf(os.Stderr, "chippy dap: listening on :%s\n", port)
		conn, err := ln.Accept()
		if err != nil {
			fmt.Fprintln(os.Stderr, "dap: accept:", err)
			os.Exit(1)
		}
		defer func() { _ = conn.Close() }()
		srv := dap.NewServer(conn, conn)
		if err := srv.Serve(); err != nil {
			fmt.Fprintln(os.Stderr, "dap:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown -dap mode %q (want stdio or tcp:PORT)\n", mode)
		os.Exit(2)
	}
}
