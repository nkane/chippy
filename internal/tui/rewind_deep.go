package tui

import (
	"fmt"
	"strconv"

	"github.com/nkane/chippy/cpu"
)

// cmdRewind handles `:rewind N` — step the machine back N executed steps.
// Small jumps that still fit in the fine ring pop exact per-step deltas;
// larger jumps restore the nearest keyframe and replay forward to the exact
// target (issue #392).
func (m *Model) cmdRewind(args []string) string {
	if m.Rewind == nil {
		return "rewind: disabled"
	}
	if len(args) == 0 {
		return "usage: :rewind N   (steps back; see :rewind-budget)"
	}
	n, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil || n == 0 {
		return fmt.Sprintf("rewind: bad count %q", args[0])
	}
	if n > m.StepCount {
		n = m.StepCount
	}
	target := m.StepCount - n
	return m.rewindToStep(target)
}

// rewindToStep moves execution back to the given absolute step index,
// updating the CPU, RAM, peripherals, StepCount, and the fine ring. Returns a
// status string.
func (m *Model) rewindToStep(target uint64) string {
	if target >= m.StepCount {
		return "rewind: already at or before that step"
	}
	delta := m.StepCount - target

	// Fast path: the fine ring still holds every step back to the target.
	if delta <= uint64(m.Rewind.Len()) {
		for i := uint64(0); i < delta; i++ {
			s, ok := m.Rewind.Pop()
			if !ok {
				break
			}
			m.CPU.Restore(s, m.RAM)
			m.restoreperipherals(s)
		}
		m.StepCount = target
		return fmt.Sprintf("rewind -> step %d ($%04X)", m.StepCount, m.CPU.PC)
	}

	// Deep path: restore the nearest keyframe at/before target, then replay
	// forward the remainder.
	kf, ok := m.Keyframes.Nearest(target)
	if !ok {
		oldest := uint64(0)
		if o, has := m.Keyframes.Oldest(); has {
			oldest = o
		}
		return fmt.Sprintf("rewind: step %d beyond reach (oldest = %d; raise :rewind-budget)",
			target, oldest)
	}
	m.CPU.Restore(kf.Snap, m.RAM)
	m.restoreperipherals(kf.Snap)
	m.StepCount = kf.Step
	m.Rewind.Reset()

	m.replayingRewind = true
	for m.StepCount < target {
		m.stepReplay()
	}
	m.replayingRewind = false
	return fmt.Sprintf("rewind -> step %d ($%04X, replayed %d from keyframe %d)",
		m.StepCount, m.CPU.PC, target-kf.Step, kf.Step)
}

// stepReplay advances one step during forward replay: it captures fine-ring
// deltas (so `<` works after landing) but skips keyframe capture and the DAP
// mutex dance — replay is always synchronous within a key handler.
func (m *Model) stepReplay() {
	snap := m.CPU.Snapshot(m.RAM)
	m.captureperipherals(&snap)
	m.RAM.ResetShadow()
	m.Source.Step()
	snap.Pages = m.RAM.TakeShadow()
	m.Rewind.Push(snap)
	m.StepCount++
}

// cmdRewindBudget handles `:rewind-budget MB` — resize the keyframe memory
// cap. Rebuilding the ring drops existing keyframes (reach restarts from the
// next keyframe), so it reports the new reach.
func (m *Model) cmdRewindBudget(args []string) string {
	if len(args) == 0 {
		return fmt.Sprintf("rewind-budget = %d MiB (%s); usage: :rewind-budget MB",
			m.RewindBudgetMB, m.rewindReachLabel())
	}
	mb, err := strconv.Atoi(args[0])
	if err != nil || mb < 1 {
		return fmt.Sprintf("rewind-budget: bad value %q (1-%d MiB)", args[0], maxRewindBudgetMB)
	}
	if mb > maxRewindBudgetMB {
		mb = maxRewindBudgetMB
	}
	m.RewindBudgetMB = mb
	m.Keyframes = cpu.NewKeyframeRing(mb << 20)
	m.seedKeyframe()
	return fmt.Sprintf("rewind-budget = %d MiB — reach ≈ %s steps, %d keyframes",
		mb, humanCount(m.rewindReachSteps()), m.Keyframes.Cap())
}

// rewindReachSteps is the maximum number of steps back a deep rewind can
// currently reach: ring capacity × keyframe interval.
func (m *Model) rewindReachSteps() uint64 {
	if m.Keyframes == nil {
		return uint64(m.Rewind.Len())
	}
	return uint64(m.Keyframes.Cap()) * keyframeInterval
}

// rewindReachLabel summarises current deep-rewind state for the status line.
func (m *Model) rewindReachLabel() string {
	return fmt.Sprintf("reach %s, %d/%d KiB-frames",
		humanCount(m.rewindReachSteps()), m.Keyframes.Len(), m.Keyframes.Cap())
}

// humanCount renders a step count compactly (1234567 -> "1.2M").
func humanCount(n uint64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return strconv.FormatUint(n, 10)
	}
}
