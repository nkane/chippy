# State-file format

chippy persists per-ROM session state to `~/.chippy/state-<rom>.json`.
A file written by chippy 1.0+ carries `"schemaVersion": 1` and lives
under the freeze promise documented here.

## Compatibility contract (v1.x)

- **A v1 writer must include `schemaVersion`.** The `internal/tui`
  loader uses the field to route reads.
- **A v1 reader accepts**:
  - `schemaVersion` absent — treated as a v0 legacy file (chippy <
    1.0). Field-compatible decode; missing fields default to zero.
  - `schemaVersion == 1` — current format.
  - `schemaVersion > 1` — silently ignored. Newer chippy may have
    persisted state the current build can't safely interpret; refusing
    to load preserves it for the newer build.
- **Adding a field inside v1.x is allowed** when the field's JSON
  default value (`""`, `0`, `false`, `null`) matches the intended
  behavior for an old file that lacks it. Examples: a new `Theme` field
  defaults to "default" via the empty-string case.
- **Removing or repurposing a field requires `schemaVersion` bump** and
  a migration code path in `loadState`.

## v1 schema

Field names match the JSON tags on `savedState` in
`internal/tui/state.go`:

| field | type | purpose |
|---|---|---|
| `schemaVersion` | int | always `1` for v1 writers |
| `breakpoints` | array of Breakpoint | source/instr/function bps incl. metadata |
| `mem_bps` | array of MemBP | memory watchpoints |
| `mem_view_addr` | uint16 | top-of-page in the memory pane |
| `mem_cursor` | uint16 | cursor position in the memory pane |
| `watches` | array of Watch | watch-panel rows (mem + reg) |
| `target_hz` | int | speed throttle (`0` = unthrottled) |
| `disasm_follow` | *bool | auto-follow-PC toggle in the disassembly pane |
| `stack_annotate` | *bool | JSR-frame annotation in the stack pane |
| `input_mode` | bool | keyboard-input-routing-to-CPU mode |
| `disasm_anchor` | uint16 | manual scroll position in the disassembly pane |
| `immediate_history` | array of ImmediateEntry | immediate-window scrollback |
| `theme` | string | active color palette (`default` / `mono` / `protan` / `tritan`) |

The pointer-typed `disasm_follow` and `stack_annotate` exist so a v0
legacy file's absence of those fields doesn't decode to `false` and
clobber the `New(c, r)` defaults (both default to `true`).

The exact JSON shape of `Breakpoint`, `MemBP`, and `Watch` is captured
by `internal/tui/testdata/state-v1.json`. The
`TestLoadState_GoldenV1` test pins that file against the current
struct definitions — any change to a JSON tag or field semantic must
also update both the golden and the schema version.

## Future migrations

When `schemaVersion` is bumped to 2:

1. Add a new constant value in `internal/tui/state.go`.
2. Branch on `s.SchemaVersion` in `loadState` and call a per-version
   migration helper (e.g. `migrateV1ToV2`) for older files.
3. Add a `testdata/state-v2.json` golden alongside the v1 one.
4. Keep the v1 golden in place — the v2 loader is contracted to read
   v1 files.
