# chippy-bp-and-run.tape
#
# Smoke test for #225 (breakpoint set + run-to-bp). Sets a bp at the
# `loop` symbol, presses `r` to run, expects the CPU to halt at the
# bp. PR review reads the GIF; CI only checks that VHS rendered
# without crashing.

Output test/smoke/out/chippy-bp-and-run.gif

Require ./chippy

Set Shell "bash"
Set FontSize 14
Set Width 1400
Set Height 800
Set Padding 12
Set TypingSpeed 30ms

Hide
Type "./chippy -rom example/count_to_ten.bin"
Enter
Sleep 1500ms
Show

# Set breakpoint at the `loop` symbol via prompt.
Type ":bp loop"
Enter
Sleep 800ms

# Run — should halt at the breakpoint.
Type "r"
Sleep 1500ms

# Step once to confirm we're at loop.
Type "s"
Sleep 800ms

# Quit cleanly.
Type ":q"
Enter
Sleep 500ms
