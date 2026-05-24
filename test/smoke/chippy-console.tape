# chippy-console.tape
#
# Smoke test for #232 / PR #275 — Quake-style drop-down console.
# Opens the console with backtick, runs a few commands through it,
# exercises PgUp / PgDn scroll, closes with backtick. PR review
# reads the GIF; CI only checks that VHS rendered without crashing.

Output test/smoke/out/chippy-console.gif

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

# Open the console.
Type "`"
Sleep 800ms

# Run pc — outputs the current PC.
Type "pc"
Enter
Sleep 500ms

# Run goto start — jumps somewhere recognizable.
Type "goto start"
Enter
Sleep 500ms

# Run pc again so the scrollback has multiple lines.
Type "pc"
Enter
Sleep 500ms

# Walk the scrollback up + down.
PageUp
Sleep 400ms
PageDown
Sleep 400ms

# Type something then backspace partially to show editing.
Type "rmw"
Sleep 300ms
Backspace
Backspace
Backspace
Sleep 300ms

# Close the console.
Type "`"
Sleep 500ms

# Quit cleanly.
Type ":q"
Enter
Sleep 500ms
