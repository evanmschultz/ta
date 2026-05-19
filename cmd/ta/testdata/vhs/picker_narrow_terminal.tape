# expect: enter submit
#
# F38d-2.4: narrow-terminal help-bar fold. VHS renders the picker in
# a real narrow viewport (Width 800 px @ FontSize 14 ≈ ~70 cols, well
# under pickerNarrowWidth=80) so the help bar splits to two lines.
# Marker "enter submit" appears on the second line of the help bar —
# proves the trailing bindings are still visible (not truncated off
# the right edge) after the fold.

Output cmd/ta/testdata/vhs/picker_narrow_terminal.gif
Output cmd/ta/testdata/vhs/picker_narrow_terminal.txt

Set Shell "bash"
Set FontSize 14
Set Width 800
Set Height 600
Set TypingSpeed 50ms
Set Framerate 24

Hide
Type 'export HOME="$(pwd)/cmd/ta/testdata/vhs/fixture_home"'
Enter
Sleep 200ms
Type 'rm -rf /tmp/ta-vhs-picker-narrow && mkdir -p /tmp/ta-vhs-picker-narrow'
Enter
Sleep 500ms
Show

Type "./bin/ta init --path /tmp/ta-vhs-picker-narrow"
Enter
Sleep 1500ms

Type "j"
Sleep 200ms
Type "l"
Sleep 300ms

Type "/"
Sleep 300ms
Type "led"
Sleep 800ms
