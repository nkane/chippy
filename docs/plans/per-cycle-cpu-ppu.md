# Per-cycle CPU↔PPU interleave (#342, tests 6-10) — ✅ COMPLETE (10/10)

**Outcome:** Blargg `ppu_vbl_nmi` passes **10/10**, now a hard accuracy
gate (no `knownFail`). Landed across PRs: P0 #365 (safety net), P1 #366
(per-cycle interleave + /NMI level model → 9/10), P3 (dot-339 odd-frame
latch → 10/10). nestest byte-identical, NMOS/CMOS untouched, demo SHAs
unchanged, perfgate holds.


Goal: pass Blargg `ppu_vbl_nmi` tests 6-10 (`suppression`, `nmi_disable`,
`nmi_timing` edge cases, `even_odd_*`). These need a `$2002` read to race
the /NMI edge at **sub-cycle** resolution — the read must land *between*
the PPU's flag-set and the CPU's interrupt sample. The current
instruction-stepped, pre-tick model (tests 2-5, shipped) can't represent
that.

## Where we are now

`cpu.Step` (NES variant): resolve addressing, pre-tick the bus ticker by
the base instruction length, poll NMI into `nmiDue` at the penultimate
cycle, tick the last cycle, run the opcode body. All PPU/APU/cart ticks
are batched at instruction granularity. `$2002` reads sample the PPU at
the instruction's data-access dot but every read in an instruction
samples the *same* dot, and the /NMI edge is pulsed by the PPU
(`TriggerNMI`) rather than driven as a level the CPU samples per cycle.

## Target model

**Decision (locked):** the per-cycle tick fans out to the **whole chain**
(PPU + APU + cart) — `busTicker.Tick(1)` per CPU cycle — for accuracy +
simplicity, accepting APU/MMC3-IRQ demo re-verify/re-pin.

True 1:1 lockstep for `VariantNES`: every CPU cycle ticks the bus chain
once (`busTicker.Tick(1)` → MMIO → PPU 3 dots / APU 1 step / cart 1),
and every CPU bus access happens on its real cycle with the PPU already
advanced to that point. The /NMI line is a **level** the PPU drives
(`vblank-flag AND PPUCTRL.7`); the CPU edge-detects + polls it each
cycle. Reading `$2002` clears the flag → line drops → if the CPU hasn't
latched the edge yet, no NMI (natural suppression).

NMOS / CMOS (chippy debugger, `WBus` ticker) keep the existing
instruction-stepped path untouched. Risk is contained to nessy.

## Architecture choice: tick-on-bus-access (Option A)

Reuse the existing opcode handlers (`opLDA`, …) and `resolve`. The 6502
performs exactly one bus access per cycle, so if every cycle has a
corresponding access and each access ticks the chain *before* it runs, we
get per-cycle interleave for free. Today's code already issues most of
the right accesses (opcode fetch in `Step`, operand bytes in `resolve`,
data access in the handler); what's missing is the **dummy** reads/writes
the 6502 does on idle/fix-up cycles. Add those per addressing-mode
template.

Rejected: full micro-step state-machine rewrite of every opcode (Option
B) — larger surface, more regression risk, no accuracy benefit over A for
these tests.

### Mechanism

1. Add `c.read(addr)` / `c.write(addr, v)` methods: for NES, tick the
   chain one cycle (`c.tick()`), then do the access; for other variants,
   just do the access (no tick).
2. Mechanically rewrite `c.Bus.Read(` → `c.read(` and `c.Bus.Write(` →
   `c.write(` across `exec.go`, `addressing.go`, and the helpers
   (`load`/`store`, `push`/`pop`, vector fetches). The opcode fetch in
   `Step` and the `resolve` operand reads become real ticked cycles.
3. Add `c.idle()` (a ticked dummy cycle) and place the missing dummy
   accesses so total cycles per instruction match the table exactly:
   - **Implied / accumulator** (2c): one dummy read of the next PC byte.
   - **Indexed read, page-crossed** (+1c): dummy read of the unfixed
     `(base & 0xFF00) | low` address before the real read.
   - **RMW** (zp/abs/abs,X): the modify cycle is a dummy **write** of the
     old value (real 6502 double-writes — visible to MMIO).
   - **Indexed write / store** (abs,X / abs,Y / (zp),Y): always one dummy
     read at the unfixed address (no page-add — these are fixed-length).
   - **Branches**: taken = +1 (dummy opcode-fetch at new PC); page-cross
     = +1 more.
   - **Stack ops, JSR / RTS / RTI / BRK, JMP (ind)**: add their known
     idle/dummy cycles to hit 3/4/6/7.
4. For NES, drop the per-instruction `busTicker.Tick`; the per-cycle
   ticks now carry the whole budget. Keep it for non-NES.

`nestest`'s golden log pins both per-instruction results **and** the
running cycle count, so any cycle-count error in a template fails loudly.

## /NMI level model

1. PPU: maintain `nmiLine = (status&0x80 != 0) && (ctrl&0x80 != 0)`.
   Recompute on the vblank set (241,1), the clear (pre-render,1), `$2002`
   reads, and `$2000` writes. Drop the `TriggerNMI` edge pulse.
2. CPU: each `c.tick()` samples the line. Edge-detect a high→asserted
   transition into `nmiPending`; the existing `nmiDue` poll (now applied
   per-cycle at the penultimate cycle) decides service. Because the
   sample happens each cycle and the `$2002` read (which drops the line)
   is itself a ticked cycle, a read that coincides with the set window
   prevents the edge from ever latching — suppression falls out.
3. Keep `TriggerNMI` as the public API for non-NES / external callers
   (maps to forcing the line). `SuppressNMI` not needed — the level model
   subsumes it.

## Interrupt poll timing

Poll on every cycle but act with the 6502's "before the final cycle"
rule: latch `nmiDue = nmiPending` on each cycle except the instruction's
last, so an edge on the last cycle waits one instruction. IRQ follows the
same per-cycle poll (keep MMC3 IRQ working — verify the split-bar demo).

## Stall / DMA integration

- OAMDMA (`pendingStall`, 513c) and DMC bus-steal: tick the chain one
  cycle per stalled cycle (already loops; convert the batched
  `Tick(stalled)` to per-cycle so PPU alignment during DMA is exact).
- DMC sample fetch already routes through MMIO; ensure its reads go
  through `c.read` so they tick.

## Risk + blast radius

- **Demo SHAs WILL churn** (PPU phase shifts vs the pre-tick model).
  Mitigation below.
- **APU per-cycle** ticking shifts audio sample timing → audio-probe
  demos (vrc6/vrc7/sunsoft/all-channels) may change. Re-verify their
  assertions (most check "emits audio / non-silent", not exact SHA).
- **MMC3 IRQ** poll moves per-cycle → mmc3-split / IRQ demos: re-verify
  structural assertions.
- **nestest** must stay byte-identical in its log (results + cycle
  counts). This is the primary correctness gate for the opcode templates.
- **Perf**: per-cycle ticking adds function-call overhead per cycle.
  `perfgate` (TestPerfGate) ceiling must hold; if not, inline `tick`.
- **Save-state**: `Step` stays atomic (one full instruction), so no
  mid-instruction save. `nmiLine` adds to PPU FullState; `nmiDue` already
  serialised.

## Test + verification strategy

1. **Reference capture (before any change):** dump the textual
   framebuffer (the `CHIPPY_DEMO_INSPECT` ascii grid) for every
   SHA-pinned demo into a committed reference file. After the rewrite,
   diff the ascii to confirm each demo still renders the *same picture*
   even though the SHA changed — then re-pin the SHA.
2. **nestest** (`-tags=nestest`) green after each phase — the cycle-exact
   guard.
3. **Klaus / decimal / BCD** unaffected (non-NES path untouched) — run to
   confirm.
4. **ppu_vbl_nmi** accuracy: drive it after each phase; expect 2-5 to
   stay green, then 6-10 to flip. Add `even_odd_frames` /
   `vbl_nmi_timing` ROMs to the harness as they come into reach.
5. New per-cycle unit tests: assert PPU `(scanline,dot)` at a given CPU
   cycle through a real instruction; assert a `$2002` read on the set dot
   suppresses NMI end-to-end (CPU+PPU).

## Phasing (each phase: build + nestest + demos green, its own PR)

- **P0 — Safety net.** ✅ Done (PR #365). Demo ascii references +
  the plan doc. No behavior change.
- **P1 — Ticked bus accesses + dummy cycles + /NMI level model.** ✅ Done.
  Folded P2 in: the per-cycle interleave shifts NMI timing, so the level
  model had to land together to avoid regressing tests 4-5. Result:
  `ppu_vbl_nmi` 5/10 → **9/10** (2-9 pass). nestest byte-identical;
  demos unchanged (correct NMI timing realigned them — no re-pin needed);
  perfgate holds. The vbl-flag dot-race (`vblSetAtDots`/`vblClearAtDots`)
  stayed — it's the flag *read value* race, orthogonal to the /NMI edge.
- **P2 — folded into P1.** (Was: /NMI level model.)
- **P3 — test 10 `even_odd_timing`.** The odd-frame pre-render dot-skip
  must observe BG-enable (`$2001`) at the exact dot. Our skip checks
  `renderingEnabled()` at the pre-render scanline; the timing of a
  mid-scanline `$2001` write vs the dot-340→0 skip decision is off by a
  few dots. PPU-side only (no CPU change). Then clear `knownFail`.
- **P4 — Cleanup + docs.** Revisit the `instrCycles` panic (keep as an
  invariant guard vs soften); README/CLAUDE notes; fold `even_odd_frames`
  / `vbl_nmi_timing` ROMs into the harness.

## Rollback

Each phase is its own PR off a feature branch; P1 is the only one that
can't be partially reverted (it's the core). If P1 destabilises, the
branch is abandoned and main stays at 5/10 — no released behavior depends
on 6-10. Demo SHA re-pins are the only main-affecting change and are
gated behind ascii verification.
