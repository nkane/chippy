package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nkane/chippy/internal/dap"
)

// runDAPAttach connects out to a remote DAP server (typically nessy
// once #179 lands), drives the initialize + attach handshake, drains
// events for a short window, and disconnects cleanly.
//
// This is Phase A of #180: the wire works end-to-end and the user can
// confirm a remote server is reachable. Phase B (CPUSource interface +
// TUI refactor) and Phase C (DAP-backed source under attach mode) live
// in follow-up PRs — the local TUI is intentionally not started here
// since it can't drive a remote CPU yet.
//
// Address forms accepted by the underlying dap.Dial:
//
//	tcp:HOST:PORT      explicit transport
//	HOST:PORT          tcp default
func runDAPAttach(addr string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := dap.Dial(ctx, addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dap-attach:", err)
		os.Exit(1)
	}
	defer func() { _ = c.Close() }()

	caps, err := c.Initialize(dap.InitializeArguments{ClientID: "chippy", AdapterID: "chippy"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "dap-attach: initialize:", err)
		os.Exit(1)
	}
	resp, err := c.Attach()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dap-attach: attach:", err)
		os.Exit(1)
	}
	if !resp.Success {
		fmt.Fprintln(os.Stderr, "dap-attach: attach refused:", resp.Message)
		os.Exit(1)
	}

	fmt.Printf("attached: %s\n", addr)
	fmt.Printf("capabilities: stepBack=%v condBp=%v readMemory=%v\n",
		caps.SupportsStepBack,
		caps.SupportsConditionalBreakpoints,
		caps.SupportsReadMemoryRequest)

	// Drain incoming events for a short window so the user can see the
	// initial `initialized` / `stopped` events the server emits after
	// attach. 2 s is enough to catch the stop-on-entry event.
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				fmt.Fprintln(os.Stderr, "dap-attach: server closed the connection")
				return
			}
			fmt.Printf("event: %s\n", ev.Event)
		case <-deadline.C:
			if err := c.Disconnect(); err != nil {
				fmt.Fprintln(os.Stderr, "dap-attach: disconnect:", err)
			}
			return
		}
	}
}
