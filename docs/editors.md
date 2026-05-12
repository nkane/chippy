# Editor integration matrix

chippy speaks the standard [Debug Adapter Protocol](https://microsoft.github.io/debug-adapter-protocol/),
so any DAP-aware editor can drive it once you point its launch
configuration at `chippy -dap stdio` (or `chippy -dap tcp:PORT`).

| Editor | Status | Notes |
|--------|--------|-------|
| **VS Code** | ✅ Tested | First-class. Install `extension/vscode-chippy/` from a `.vsix`; example config at `examples/dap/launch.json`. |
| **nvim-dap** | ✅ Tested | Example adapter + configuration at `examples/dap/nvim-dap.lua`. Uses the TCP transport. |
| **Cursor** | ✅ Compatible | Same extension as VS Code; install the `.vsix`. |
| **JetBrains (IDEA / GoLand / CLion)** | 🟡 Unverified | DAP supported via the "Debug Adapter Protocol" plugin; no chippy-specific config tested yet. |
| **Helix** | 🟡 Unverified | DAP supported; debugger config goes in `~/.config/helix/languages.toml`. No chippy-specific tested config. |
| **vimspector** | 🟡 Unverified | DAP supported; users can adapt `examples/dap/nvim-dap.lua` to vimspector's JSON format. |
| **emacs (dap-mode)** | 🟡 Unverified | DAP supported; no chippy-specific tested config. |
| **Sublime Text (Debugger)** | 🟡 Unverified | DAP supported; needs the LSP-Debugger plugin family. |

Legend: ✅ = a chippy maintainer has run the editor against chippy in
the last release cycle. 🟡 = the editor speaks DAP and *should* work
but no one has dogfooded it yet — your report (or PR adding a tested
config) is welcome.

## Adding your editor

The `launch.json` shape is small — see `examples/dap/launch.json` for
the VS Code form. Most other editors take the same fields:

| field | meaning |
|-------|---------|
| `rom` | path to `.bin` / `.prg` / `.hex` / `.o` |
| `loadAddr` | load address for raw `.bin` (decimal) |
| `cpuVariant` | `"nmos"` or `"65c02"` |
| `dbgPath` | path to a cc65 `.dbg` (auto-detected from `<rom>.dbg` if omitted) |
| `tracePath` | optional trace-file destination |
| `stopOnEntry` | `true` (default) pauses at reset; `false` auto-starts |

If you wire chippy into a new editor, please open a PR moving the row
from 🟡 to ✅ and adding a sample config under `examples/dap/`.
