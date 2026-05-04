# expect: FIXTURE_HOME_PRESENT
#
# F38d-1 VHS smoke tape. Establishes the per-tape `# expect:` marker
# convention every later sub-slice's tape inherits. The marker is the
# stable post-action terminal text greppable in the produced .txt
# artifact — here we just `Type` it because no production model
# exists yet to render anything else. F38d-2..-5 tapes will pin
# markers to model-rendered text (filter status lines, toggle
# prefixes, etc.) instead.

Output cmd/ta/testdata/vhs/smoke.gif
Output cmd/ta/testdata/vhs/smoke.txt

Set Shell "bash"
Set FontSize 14
Set Width 800
Set Height 400
Set TypingSpeed 50ms
Set Framerate 24

Type "FIXTURE_HOME_PRESENT"
Sleep 200ms
Enter
Sleep 200ms
