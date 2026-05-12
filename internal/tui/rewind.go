package tui

import "github.com/nkane/chippy/internal/cpu"

// Thin wrappers around cpu.SnapshotRing so existing tui code that
// references `rewindRing` / `newRewindRing` / `defaultRewindCap` keeps
// working. The actual ring lives in the cpu package so the DAP server
// (and any future consumer) can share it.

const defaultRewindCap = cpu.DefaultSnapshotRingCap

type rewindRing = cpu.SnapshotRing

func newRewindRing(cap int) *rewindRing {
	return cpu.NewSnapshotRing(cap)
}
