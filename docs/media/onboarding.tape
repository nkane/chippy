# onboarding.tape — vhs script for docs/media/onboarding.gif
# https://github.com/charmbracelet/vhs
#
# Run from repo root:
#   vhs docs/media/onboarding.tape
#
# Output lands at docs/media/onboarding.gif (set by `Output` below).
# vhs handles altscreen + Bubble Tea cleanly where asciinema/agg
# struggle — it drives the PTY directly with deterministic keystrokes.

Output docs/media/onboarding.gif

Set Shell "zsh"
# chippy's TUI needs roughly 130 cols × 45 rows of cells to render
# every panel without clipping. Smaller font + larger window gives
# the layout room to breathe; the resulting GIF compresses fine.
Set FontSize 12
Set Width 1600
Set Height 1000
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

# Launch chippy on a small ca65 demo. The reset state is paused at $8000.
Type "export COLORTERM=truecolor && chippy -rom example/c/hello.bin"
Enter
Sleep 2s

# Step through the first few instructions. Each `s` advances one
# instruction; the reg pane updates in real time.
Type "s"
Sleep 600ms
Type "s"
Sleep 600ms
Type "s"
Sleep 600ms
Type "s"
Sleep 1s

# Free-run until the program reaches the JMP halt, then pause.
Type "r"
Sleep 2s
Type "r"
Sleep 1s

# Reverse-step once to demonstrate the rewind ring.
Type "<"
Sleep 1500ms

# Pop the help modal so the keybinding surface is visible in the GIF.
Type "?"
Sleep 2s
# Close the modal (any key).
Space
Sleep 500ms

# Quit cleanly.
Type "q"
Sleep 500ms
