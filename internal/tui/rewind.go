package tui

import "github.com/nkane/chippy/cpu"

// Thin wrappers around cpu.SnapshotRing so existing tui code that
// references `rewindRing` / `newRewindRing` / `defaultRewindCap` keeps
// working. The actual ring lives in the cpu package so the DAP server
// (and any future consumer) can share it.

const defaultRewindCap = cpu.DefaultSnapshotRingCap

// Deep-rewind tuning (issue #392).
//
//	keyframeInterval — steps between full-RAM keyframes. Forward-replay after
//	  a deep rewind costs at most this many steps, so it bounds latency; a
//	  larger value buys more reach per byte of budget. 4096 6502 instructions
//	  replay in well under a millisecond, far inside the 100 ms target.
//	defaultRewindBudgetMB — cap on keyframe memory. Reach in steps is
//	  (budgetMB·1MiB / 64KiB) · interval. At 128 MiB that's 2048 · 4096 ≈
//	  8.4M steps; raise with `:rewind-budget` (256 MiB ≈ 16.7M). Memory is a
//	  cap, not a reservation — a short run holds only the keyframes it made.
const keyframeInterval uint64 = 4096

const (
	defaultRewindBudgetMB = 128
	maxRewindBudgetMB     = 1024
)

type rewindRing = cpu.SnapshotRing

func newRewindRing(cap int) *rewindRing {
	return cpu.NewSnapshotRing(cap)
}
