package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/nkane/chippy/symbols"
)

// runNessyLauncher spawns a nessy child process, waits for its DAP
// listener to come up, dials + handshakes with stopOnEntry=true so
// the game is paused at the reset vector, then opens the TUI. On
// TUI exit the child is signalled to shut down.
//
// One-shell UX:
//
//	chippy -nessy game.nes
//
// nessy's window opens; the chippy TUI opens in the same terminal
// (alt-screen). User drives execution with `s` / `r` / `b`. Pressing
// `q` quits the TUI and tears down the game window.
func runNessyLauncher(romPath, nessyBin string) {
	bin, err := resolveNessyBinary(nessyBin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "chippy -nessy:", err)
		os.Exit(1)
	}

	port, err := pickFreePort()
	if err != nil {
		fmt.Fprintln(os.Stderr, "chippy -nessy: pick port:", err)
		os.Exit(1)
	}

	// `-wait-for-debugger` keeps nessy's game loop paused at the reset
	// vector until our attach lands. Without it the launcher's spawn
	// + dial + handshake takes ~100 ms while the game loop races
	// ahead, ending up past the reset routine before we can issue
	// the first stopped event.
	cmd := exec.Command(bin, "-rom", romPath, "-dap-port", strconv.Itoa(port), "-wait-for-debugger")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "chippy -nessy: spawn:", err)
		os.Exit(1)
	}

	// If the child crashes or exits before we've cleaned up the TUI,
	// the deferred kill should not error out — log and continue.
	defer func() {
		if cmd.Process != nil {
			_ = killChild(cmd)
		}
	}()

	addr := fmt.Sprintf("tcp:127.0.0.1:%d", port)
	if err := waitForListener(port, 5*time.Second); err != nil {
		_ = killChild(cmd)
		fmt.Fprintln(os.Stderr, "chippy -nessy: nessy DAP listener never came up:", err)
		os.Exit(1)
	}

	client, err := dialAndHandshake(addr, true)
	if err != nil {
		_ = killChild(cmd)
		fmt.Fprintln(os.Stderr, "chippy -nessy:", err)
		os.Exit(1)
	}

	// Source view + symbol resolution. Both processes run on the
	// same machine and the .dbg references local file paths, so the
	// cheapest path is to load it ourselves rather than wire DAP
	// `source` / `loadedSources` requests just yet. nessy already
	// loaded the same file on its side; the redundancy is fine.
	syms, srcMap := loadSiblingDbg(romPath)

	runAttachedTUI(attachedTUIConfig{
		client:     client,
		addr:       addr,
		status:     fmt.Sprintf("nessy: %s (paused — press r to run, s to step)", filepath.Base(romPath)),
		syms:       syms,
		srcMap:     srcMap,
		showSource: srcMap != nil, // source-view first if we have line info
		onExit:     []func(){func() { _ = killChild(cmd) }},
	})
}

// loadSiblingDbg auto-detects a `.dbg` next to the ROM and loads the
// symbol table + source map. Missing or unparseable files surface as
// warnings, not fatal errors — the launcher still opens the TUI,
// just without source-view.
func loadSiblingDbg(romPath string) (*symbols.Table, *symbols.SourceMap) {
	dbg := symbols.SiblingDbg(romPath)
	if dbg == "" {
		return nil, nil
	}
	var syms *symbols.Table
	var srcMap *symbols.SourceMap
	if t, err := symbols.LoadDbg(dbg); err != nil {
		fmt.Fprintln(os.Stderr, "chippy -nessy: load dbg:", err)
	} else {
		syms = t
	}
	if sm, err := symbols.LoadSourceMap(dbg); err != nil {
		fmt.Fprintln(os.Stderr, "chippy -nessy: load source map:", err)
	} else {
		srcMap = sm
	}
	return syms, srcMap
}

// resolveNessyBinary picks the nessy executable. Search order:
//
//  1. explicit override (`-nessy-binary PATH`).
//  2. `nessy` on $PATH.
//  3. sibling of the running chippy executable (same directory).
//
// Returns an error with all three failures noted when none of the
// candidates exist + execute.
func resolveNessyBinary(override string) (string, error) {
	if override != "" {
		if _, err := os.Stat(override); err == nil {
			return override, nil
		}
		return "", fmt.Errorf("-nessy-binary %q: %w", override, errors.New("not found"))
	}
	// Prefer a sibling of the running chippy binary before falling back
	// to $PATH. Stale system-wide nessy installs shouldn't shadow a
	// fresh local build the user just produced from the same checkout.
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), nessyBaseName())
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	if p, err := exec.LookPath("nessy"); err == nil {
		return p, nil
	}
	return "", errors.New(
		"nessy binary not found — install via `go install -tags=nessy github.com/nkane/chippy/cmd/nessy@latest`, " +
			"or pass `-nessy-binary PATH`")
}

func nessyBaseName() string {
	if runtime.GOOS == "windows" {
		return "nessy.exe"
	}
	return "nessy"
}

// pickFreePort grabs an ephemeral TCP port the kernel picks for us
// and immediately closes the listener. There's a small race window
// where another process could re-grab the port before nessy claims
// it; in practice on a developer machine this is negligible.
func pickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// waitForListener polls localhost:port until a TCP dial succeeds or
// the deadline expires. Used to gate the chippy-side DAP handshake on
// nessy's listener being ready — without it the dial loses a race
// with nessy's startup (open ROM, parse, build NES, start DAP
// listener) which can take 50-200 ms on first run.
func waitForListener(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	target := fmt.Sprintf("127.0.0.1:%d", port)
	for {
		conn, err := net.DialTimeout("tcp", target, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("dial %s: %w", target, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// killChild sends a graceful signal first and falls back to a hard
// kill if the process doesn't exit within 2 s. On Windows there's no
// SIGTERM; `Process.Kill()` is the only option.
func killChild(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		return cmd.Process.Kill()
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		return cmd.Process.Kill()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(2 * time.Second):
		return cmd.Process.Kill()
	}
}
