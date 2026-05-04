# expect: pick a subcommand
#
# F38d-5: bubbletea root menu. Bare `ta` on a TTY drives the
# new menuModel; subcommand list shows + cursor lands on first
# row. Marker is the rendered title "ta — pick a subcommand".

Output cmd/ta/testdata/vhs/menu.gif
Output cmd/ta/testdata/vhs/menu.txt

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 600
Set TypingSpeed 50ms
Set Framerate 24

Type "./bin/ta"
Enter
Sleep 2s
