# nessy-attach.tape
#
# Smoke test for #220 / #224 (nessy launcher + -wait-for-debugger).
# Spawns nessy via the chippy launcher, attaches over DAP, verifies
# the TUI lands with source visible at the reset vector and the game
# window stays blank until step. PR review reads the GIF.
#
# Requires both ./chippy and ./nessy in the repo root (Makefile builds
# both before running this tape).

Output test/smoke/out/nessy-attach.gif

Require ./chippy
Require ./nessy

Set Shell "bash"
Set FontSize 14
Set Width 1400
Set Height 800
Set Padding 12
Set TypingSpeed 30ms

Hide
Type "./chippy -nessy roms/demos/hello-bg/hello-bg.nes"
Enter
Sleep 3000ms
Show

# Source panel should already be visible (launcher loads sibling .dbg).
Sleep 1000ms

# Step a few times — game window should remain blank because nessy
# is paused-at-reset until we let it run.
Type "s"
Sleep 500ms
Type "s"
Sleep 500ms
Type "s"
Sleep 800ms

# Quit cleanly (also tears down the spawned nessy process).
Type ":q"
Enter
Sleep 800ms
