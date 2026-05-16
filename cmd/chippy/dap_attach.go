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
// Mutually exclusive with -rom, -dap, and -nessy (enforced in main).
func runDAPAttach(addr string) {
	client, err := dialAndHandshake(addr, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dap-attach:", err)
		os.Exit(1)
	}
	runAttachedTUI(client, addr, fmt.Sprintf("attached: %s", addr))
}

// dialAndHandshake opens a DAP connection and drives initialize +
// attach. Returns the live client ready for the TUI to consume.
// `stopOnEntry` controls whether to ask the server to emit a stopped
// event immediately — `-nessy` wants that so the launched game pauses
// at the reset vector; `-dap-attach` keeps the legacy "don't pause an
// already-running session" default.
func dialAndHandshake(addr string, stopOnEntry bool) (*dap.Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := dap.Dial(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	if _, err := client.Initialize(dap.InitializeArguments{ClientID: "chippy", AdapterID: "chippy"}); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	attachArgs := map[string]any{}
	if stopOnEntry {
		attachArgs["stopOnEntry"] = true
	}
	resp, err := client.Request("attach", attachArgs)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("attach: %w", err)
	}
	if !resp.Success {
		_ = client.Close()
		return nil, fmt.Errorf("attach refused: %s", resp.Message)
	}
	return client, nil
}

// runAttachedTUI builds the mirror CPU + RAM, wraps the live client in
// a RemoteSource, refreshes initial register state, and runs the TUI
// to completion. On exit (clean or error) the RemoteSource is closed
// — which sends a DAP `disconnect` and tears down the wire.
//
// `onExit`, if non-nil, runs after RemoteSource.Close() and before
// returning to the caller. The launcher uses it to SIGTERM the nessy
// child process so the game window goes away when the TUI quits.
func runAttachedTUI(client *dap.Client, addr, initialStatus string, onExit ...func()) {
	// Mirror state. The TUI's display panels read these fields
	// directly; RemoteSource writes them after every step / refresh.
	ram := cpu.NewRAM()
	mirror := cpu.NewVariant(ram, cpu.VariantNMOS)

	source := tui.NewRemoteSource(client, mirror, ram, addr)
	if err := source.RefreshRegs(); err != nil {
		fmt.Fprintln(os.Stderr, "chippy: initial register sync:", err)
		// Continue anyway — the next stopped event will refresh.
	}

	model := tui.New(mirror, ram).WithSource(source)
	model.Status = initialStatus

	p := tea.NewProgram(model, tea.WithAltScreen())
	_, runErr := p.Run()
	_ = source.Close()
	for _, fn := range onExit {
		if fn != nil {
			fn()
		}
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "chippy:", runErr)
		os.Exit(1)
	}
}
