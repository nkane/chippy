-- nvim-dap adapter + sample configuration for chippy.
--
-- Drop this into your nvim config (e.g. ~/.config/nvim/lua/dap-chippy.lua)
-- and `require("dap-chippy")` somewhere your dap setup runs. Adjust the
-- `command` if `chippy` isn't on $PATH.

local dap = require("dap")

dap.adapters.chippy = {
  type = "server",
  port = "${port}",
  executable = {
    command = "chippy",
    args = { "-dap", "tcp:${port}" },
  },
}

dap.configurations["asm_ca65"] = {
  {
    type = "chippy",
    request = "launch",
    name = "Run chippy (NMOS)",
    rom = "${workspaceFolder}/build/program.bin",
    cpuVariant = "nmos",
    dbgPath = "${workspaceFolder}/build/program.dbg",
    stopOnEntry = true,
  },
  {
    type = "chippy",
    request = "launch",
    name = "Run chippy (CMOS 65C02)",
    rom = "${workspaceFolder}/build/program.bin",
    cpuVariant = "65c02",
    dbgPath = "${workspaceFolder}/build/program.dbg",
    stopOnEntry = true,
  },
}

-- ca65 source files are typically `.s` with no dedicated filetype. Map them
-- onto the chippy configurations under whichever filetype your editor
-- assigns. The block below is conservative — drop or extend to taste.
dap.configurations.asm = dap.configurations["asm_ca65"]
dap.configurations.ca65 = dap.configurations["asm_ca65"]

-- Suggested keymaps (no leader required so they work in any setup).
-- Drop these into your own keybinding init if you don't want them
-- defined globally.
local map = vim.keymap.set
map("n", "<F5>", function() dap.continue() end, { desc = "DAP continue" })
map("n", "<F10>", function() dap.step_over() end, { desc = "DAP step over" })
map("n", "<F11>", function() dap.step_into() end, { desc = "DAP step into" })
map("n", "<S-F11>", function() dap.step_out() end, { desc = "DAP step out" })
map("n", "<F9>", function() dap.toggle_breakpoint() end, { desc = "DAP toggle bp" })
