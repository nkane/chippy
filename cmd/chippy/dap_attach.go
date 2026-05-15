package main

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/dap"
	"github.com/nkane/chippy/internal/tui"
)

// runDAPAttach connects out to a remote DAP server (typically nessy),
// drives the initialize + attach handshake, then opens the TUI in
// remote-attach mode. Step keys (s / n / f) send DAP `stepIn` / `next`
// / `stepOut`; r toggles continue / pause; b syncs breakpoints across
// the wire; the q key disconnects cleanly. Display panels read from a
// local mirror CPU + RAM that the RemoteSource keeps in sync via
// post-step DAP requests.
//
// Address forms accepted by the underlying dap.Dial:
//
//	tcp:HOST:PORT      explicit transport
//	HOST:PORT          tcp default
//
// Mutually exclusive with -rom and -dap (enforced in main).
func runDAPAttach(addr string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := dap.Dial(ctx, addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dap-attach:", err)
		os.Exit(1)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(dap.InitializeArguments{ClientID: "chippy", AdapterID: "chippy"}); err != nil {
		fmt.Fprintln(os.Stderr, "dap-attach: initialize:", err)
		os.Exit(1)
	}
	resp, err := client.Attach()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dap-attach: attach:", err)
		os.Exit(1)
	}
	if !resp.Success {
		fmt.Fprintln(os.Stderr, "dap-attach: attach refused:", resp.Message)
		os.Exit(1)
	}

	// Mirror state. The TUI's display panels read these fields
	// directly; RemoteSource writes them after every step / refresh.
	// Initial values are placeholder zeros — refreshed before the TUI
	// boots so the first frame renders the live remote PC + regs.
	ram := cpu.NewRAM()
	mirror := cpu.NewVariant(ram, cpu.VariantNMOS)

	source := tui.NewRemoteSource(client, mirror, ram, addr)
	if err := source.RefreshRegs(); err != nil {
		fmt.Fprintln(os.Stderr, "dap-attach: initial register sync:", err)
		// Continue anyway — the next stopped event will refresh.
	}

	model := tui.New(mirror, ram).WithSource(source)
	model.Status = fmt.Sprintf("attached: %s", addr)

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "dap-attach:", err)
		_ = source.Close()
		os.Exit(1)
	}
	_ = source.Close()
}
