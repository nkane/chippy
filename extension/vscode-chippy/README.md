# Chippy 6502 Debugger — VS Code Extension

Registers the `chippy` debug type so VS Code knows how to launch the
[chippy](https://github.com/nkane/chippy) 6502 debugger via DAP. Without
this extension, a `launch.json` that names `"type": "chippy"` fails with
"configured debug type 'chippy' is not supported".

## Build

```sh
npm install
npm run compile
npm run package        # produces vscode-chippy-<version>.vsix
```

## Install locally

```sh
code --install-extension vscode-chippy-0.1.0.vsix
```

## Use

The extension expects `chippy` on `PATH`. Override via the `chippy.binaryPath` setting if your install lives elsewhere.

Example `.vscode/launch.json`:

```json
{
  "version": "0.2.0",
  "configurations": [
    {
      "type": "chippy",
      "request": "launch",
      "name": "chippy: run ROM",
      "rom": "${workspaceFolder}/build/program.bin",
      "cpuVariant": "nmos",
      "stopOnEntry": true
    }
  ]
}
```

See [`docs/dap.md`](../../docs/dap.md) in the chippy repo for the full
list of launch attributes.

## What it does

- Registers the `chippy` debug type via `package.json`'s `contributes.debuggers`.
- Provides a `DebugAdapterDescriptorFactory` that spawns `chippy -dap stdio`
  whenever VS Code starts a `chippy` debug session.
- Ships configuration snippets so `Add Configuration…` in `launch.json`
  offers chippy templates.

That's it — the extension is purely glue between VS Code's DAP machinery
and the chippy binary. All debugger features (stepping, breakpoints,
disassembly, reverse-step, etc.) are implemented in chippy itself.
