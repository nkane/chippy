# chippy-syms.tape
#
# Smoke test for #226 (:syms modal). Opens the modal, scrolls,
# filters, toggles a breakpoint, closes. PR review reads the GIF;
# CI only checks that VHS rendered without crashing.

Output test/smoke/out/chippy-syms.gif

Require ./chippy

Set Shell "bash"
Set FontSize 14
Set Width 1400
Set Height 800
Set Padding 12
Set TypingSpeed 30ms
Set Theme "Catppuccin Mocha"

Hide
Type "export COLORTERM=truecolor && ./chippy -rom example/count_to_ten.bin"
Enter
Sleep 1500ms
Show

# Open :syms modal via prompt.
Type ":syms"
Enter
Sleep 800ms

# Navigate down two rows.
Type "j"
Sleep 200ms
Type "j"
Sleep 400ms

# Filter — only matches containing "lo" survive.
Type "lo"
Sleep 800ms

# Toggle a breakpoint at the highlighted row.
Enter
Sleep 600ms

# Close modal.
Escape
Sleep 600ms

# Quit cleanly.
Type ":q"
Enter
Sleep 500ms
