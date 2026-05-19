# expect: Fill db
#
# L3-G9-D3b: `ta schema --action=create --kind=db --name=<name>` on
# a TTY drops into the bubbletea schema-mutation form built off the
# embedded meta-schema (description + paths). The tape sets up an
# empty project (.ta/ exists but schema.toml is comment-only), then
# fires `ta schema --action=create --kind=db --name=plans` so the
# form appears. Marker is "Fill db" — proves newSchemaMutateForm
# wired the meta-schema's `db` SectionType through FormFor cleanly.

Output cmd/ta/testdata/vhs/schema_create_db.gif
Output cmd/ta/testdata/vhs/schema_create_db.txt

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 600
Set TypingSpeed 50ms
Set Framerate 24

Hide
Type 'rm -rf /tmp/ta-vhs-schema-create && mkdir -p /tmp/ta-vhs-schema-create/.ta'
Enter
Sleep 200ms
Type 'cat > /tmp/ta-vhs-schema-create/.ta/schema.toml << "EOF"'
Enter
Type '# project schema — empty bootstrap'
Enter
Type 'EOF'
Enter
Sleep 500ms
Show

Type "./bin/ta schema --action=create --kind=db --name=plans --path /tmp/ta-vhs-schema-create"
Enter
Sleep 2s
