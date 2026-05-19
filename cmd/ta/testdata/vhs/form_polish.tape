# expect: ▸ alpha
#
# L3-G9-D2 (F13) form polish tape. Renders the polished bubbletea
# formModel against a 3-field schema spanning Input + Select + Confirm
# so the new active-cursor glyph (▸), the Cherry-styled required `*`
# marker, and the per-block spacing all land in the recorded .txt.
# The marker pins the polished cursor glyph on the first field
# (alphabetically "alpha", required) — a regression to the old `▶`
# glyph or to dropped per-field labels fails Vhs loud.

Output cmd/ta/testdata/vhs/form_polish.gif
Output cmd/ta/testdata/vhs/form_polish.txt

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 600
Set TypingSpeed 50ms
Set Framerate 24

Hide
Type 'rm -rf /tmp/ta-vhs-form-polish && mkdir -p /tmp/ta-vhs-form-polish/.ta'
Enter
Sleep 200ms
Type 'cat > /tmp/ta-vhs-form-polish/.ta/schema.toml << "EOF"'
Enter
Type '[plans]'
Enter
Type 'paths = ["plans.toml"]'
Enter
Type ''
Enter
Type '[plans.task]'
Enter
Type 'description = "A task."'
Enter
Type ''
Enter
Type '[plans.task.fields]'
Enter
Type ''
Enter
Type '[plans.task.fields.alpha]'
Enter
Type 'type = "string"'
Enter
Type 'required = true'
Enter
Type ''
Enter
Type '[plans.task.fields.choice]'
Enter
Type 'type = "string"'
Enter
Type 'enum = ["todo", "done"]'
Enter
Type ''
Enter
Type '[plans.task.fields.flag]'
Enter
Type 'type = "boolean"'
Enter
Type 'EOF'
Enter
Sleep 500ms
Show

Type "./bin/ta create plans.t1 --type plans.task --path /tmp/ta-vhs-form-polish"
Enter
Sleep 2s
Tab
Sleep 1s
