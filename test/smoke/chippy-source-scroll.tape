# chippy-source-scroll.tape
#
# Smoke test for #227 (source/disasm dual-mode scroll). Exercises the
# source view toggle and the `[ ] { } '` scroll keys. PR review reads
# the GIF; CI only checks that VHS rendered without crashing.

Output test/smoke/out/chippy-source-scroll.gif

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

# Toggle source panel.
Type "v"
Sleep 800ms

# Scroll down one line.
Type "]"
Sleep 500ms

# Scroll down eight more.
Type "}"
Sleep 500ms

# Scroll back up.
Type "{"
Sleep 500ms

# Re-follow PC.
Type "'"
Sleep 800ms

# Toggle back to disasm.
Type "v"
Sleep 500ms

# Quit cleanly.
Type ":q"
Enter
Sleep 500ms
