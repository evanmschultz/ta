# expect: Schemas (3/3)
#
# F38d-2: select-all-in-group via `space`. Cursor on the Schemas
# group header; press `space` to mark every leaf in the group.
# Marker is the group header counter flipping to "(3/3)".

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

Type "j"
Sleep 200ms
Enter
Sleep 300ms
Up
Sleep 200ms
Space
Sleep 800ms
