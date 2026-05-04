# expect: Fill task
#
# F38d-3: bubbletea formModel renders interactive create. The
# tape sets up a tiny project schema with a task type, then runs
# `ta create plans.t1 --type plans.task` without --data so the
# form fires on TTY. Marker is the rendered form title prefix
# "Fill task" — proves the new formModel substituted the huh form
# cleanly.

Output cmd/ta/testdata/vhs/form_create.gif
Output cmd/ta/testdata/vhs/form_create.txt

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 600
Set TypingSpeed 50ms
Set Framerate 24

Hide
Type 'rm -rf /tmp/ta-vhs-form && mkdir -p /tmp/ta-vhs-form/.ta'
Enter
Sleep 200ms
Type 'cat > /tmp/ta-vhs-form/.ta/schema.toml << "EOF"'
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
Type '[plans.task.fields.id]'
Enter
Type 'type = "string"'
Enter
Type 'required = true'
Enter
Type ''
Enter
Type '[plans.task.fields.status]'
Enter
Type 'type = "string"'
Enter
Type 'required = true'
Enter
Type 'EOF'
Enter
Sleep 500ms
Show

Type "./bin/ta create plans.t1 --type plans.task --path /tmp/ta-vhs-form"
Enter
Sleep 2s
