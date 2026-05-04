# expect: Overwrite existing
#
# F38d-4: bubbletea confirm renders the overwrite prompt when
# `ta template apply` lands on a destination path that already
# exists. Marker is the rendered title prefix "Overwrite existing"
# — proves the confirm model substituted huh.NewConfirm cleanly.

Output cmd/ta/testdata/vhs/confirm_overwrite.gif
Output cmd/ta/testdata/vhs/confirm_overwrite.txt

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
Type 'rm -rf /tmp/ta-vhs-confirm && mkdir -p /tmp/ta-vhs-confirm/.ta'
Enter
Sleep 200ms
Type 'echo "[plans]" > /tmp/ta-vhs-confirm/.ta/schema.toml'
Enter
Sleep 500ms
Show

Type "./bin/ta template apply plans --path /tmp/ta-vhs-confirm"
Enter
Sleep 4s
