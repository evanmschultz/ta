# expect: Schemas (0/3)
#
# F38d-2: bubbletea picker initial render against the fixture-home
# library. Marker is the Schemas group header (collapsed, 0 of 3
# leaves) — proves the picker renders the multi-category bucket
# header line for the three fixture dbs. Tape ends with the picker
# still on screen; vhs SIGTERMs `ta init` when the tape closes.

Output cmd/ta/testdata/vhs/picker_project.gif
Output cmd/ta/testdata/vhs/picker_project.txt

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
Type 'rm -rf /tmp/ta-vhs-picker-project && mkdir -p /tmp/ta-vhs-picker-project'
Enter
Sleep 500ms
Show

Type "./bin/ta init --path /tmp/ta-vhs-picker-project"
Enter
Sleep 2s
