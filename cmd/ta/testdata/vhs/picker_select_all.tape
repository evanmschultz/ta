# expect: Schemas (3/3)
#
# F38d-2 + L3-G1-D1a: select-all-in-group via `x`. Cursor on the
# Schemas group header; press `x` to mark every leaf in the group.
# Marker is the group header counter flipping to "(3/3)". Note that
# L3-G1-D1a flipped the keymap — `space` is toggle, `enter` is
# submit, `x` is select-all-in-group, `ctrl+a` is select-all-across-
# groups (the latter pinned by picker_select_all_all_groups.tape).

Output cmd/ta/testdata/vhs/picker_select_all.gif
Output cmd/ta/testdata/vhs/picker_select_all.txt

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 600
Set TypingSpeed 50ms
Set Framerate 24

Hide
Type 'export HOME="$(pwd)/cmd/ta/testdata/vhs/fixture_home"'
Enter
Sleep 200ms
Type 'rm -rf /tmp/ta-vhs-picker-select-all && mkdir -p /tmp/ta-vhs-picker-select-all'
Enter
Sleep 500ms
Show

Type "./bin/ta init --path /tmp/ta-vhs-picker-select-all"
Enter
Sleep 1500ms

Type "l"
Sleep 300ms
Type "x"
Sleep 800ms
