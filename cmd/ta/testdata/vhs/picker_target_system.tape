# expect: Bootstrap
#
# F38d-2: --target-system bootstrap picker. Empties HOME so the
# picker pulls from binary-shipped defaults (the canonical home-
# bootstrap path). Marker is the "Bootstrap" prefix on the
# multi-cat picker title line — proves the target-system flow
# reaches the same bubbletea picker model.

Output cmd/ta/testdata/vhs/picker_target_system.gif
Output cmd/ta/testdata/vhs/picker_target_system.txt

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 600
Set TypingSpeed 50ms
Set Framerate 24

Hide
Type 'rm -rf /tmp/ta-vhs-picker-target && mkdir -p /tmp/ta-vhs-picker-target'
Enter
Sleep 200ms
Type 'export HOME=/tmp/ta-vhs-picker-target'
Enter
Sleep 500ms
Show

Type "./bin/ta init --target-system"
Enter
Sleep 2s
