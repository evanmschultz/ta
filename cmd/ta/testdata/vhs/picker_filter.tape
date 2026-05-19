# expect: 1 of 3 visible
#
# F38d-2: filter mode. Press `/` then type a substring; the picker
# enters filterMode and renders the filter prompt + visible-count
# status line. Marker is "1 of 3 visible" — proves the filter
# narrowed leaves to a single match (ledger).

Output cmd/ta/testdata/vhs/picker_filter.gif
Output cmd/ta/testdata/vhs/picker_filter.txt

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
Type 'rm -rf /tmp/ta-vhs-picker-filter && mkdir -p /tmp/ta-vhs-picker-filter'
Enter
Sleep 500ms
Show

Type "./bin/ta init --path /tmp/ta-vhs-picker-filter"
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
