# Perf baseline + regression gate

chippy ships a small benchmark suite under `internal/cpu/` plus a CI
gate (issue #113) that fails when a PR regresses any of them past the
tolerance.

## Benchmarks

- `BenchmarkStep_NMOS` — bare NMOS `cpu.Step()` throughput against a
  64 KiB sea of NOPs. Baseline for "how fast can the emulator march
  through instructions".
- `BenchmarkStep_CMOS` — same loop on the 65C02 variant.
- `BenchmarkStep_WithSnapshot` — Step with the CoW page shadow
  enabled, taking a delta on each iteration. Models the cost of
  reverse-step recording (TUI tickMsg loop, DAP runLoop). Issue #66.

## How the gate works

`internal/cpu/perfgate_test.go` lives behind the `perfgate` build tag
so it stays out of the default `go test` path. It loads
`internal/cpu/testdata/perf-baseline.json` and runs each benchmark via
`testing.Benchmark()`. A benchmark fails if its measured `ns/op`
exceeds the entry's `ns_op_max` by more than 15%.

CI runs:

```sh
go test -tags=perfgate -timeout 5m -run TestPerfGate -v ./internal/cpu/...
```

The `perf baseline` job in `.github/workflows/ci.yml` exercises it on
every push.

## Refresh procedure

The numbers in `testdata/perf-baseline.json` are absolute upper bounds
chosen with headroom for typical ubuntu-latest CI runners. Refresh
when:

- a deliberate slowdown lands and the gate starts spuriously failing,
- a performance improvement makes the bounds embarrassingly loose, or
- the CI runner image changes substantially.

Recipe:

```sh
go test -bench=. -benchtime=3s -count=5 -run=^$ ./internal/cpu/ > /tmp/bench.txt
```

Take the median `ns/op` across the five runs for each benchmark, then
pick an `ns_op_max` with ~3× headroom so cross-runner variability
doesn't trip the gate. Document the change in a `chore(perf): refresh
baseline` commit with the raw numbers in the body.

## What we deliberately don't gate

- Memory allocations (`B/op`, `allocs/op`): noisy on shared runners.
- DAP / TUI throughput: bench targets are the emulator core; UI / DAP
  responsiveness is human-perceptible, not microbenchmark-friendly.
- Cold-start latency: gated indirectly by the build-time check; not a
  hot path for a debugger.
