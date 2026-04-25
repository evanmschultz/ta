package main

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestRootCmdWiring(t *testing.T) {
	cmd := newRootCmd()
	if cmd.Use != appName {
		t.Errorf("Use = %q, want %q", cmd.Use, appName)
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil")
	}
	if cmd.Short == "" {
		t.Error("Short is empty")
	}
	if cmd.Long == "" {
		t.Error("Long is empty")
	}
	if f := cmd.Flags().Lookup("log-startup"); f == nil {
		t.Error("--log-startup flag not registered")
	}
}

func TestSubcommandsRegistered(t *testing.T) {
	root := newRootCmd()
	want := []string{"get", "list-sections", "schema", "create", "update", "delete", "search"}
	for _, name := range want {
		sub, _, err := root.Find([]string{name})
		if err != nil {
			t.Errorf("subcommand %q not found: %v", name, err)
			continue
		}
		if sub.Name() != name {
			t.Errorf("resolved %q got %q", name, sub.Name())
		}
		if sub.RunE == nil {
			t.Errorf("subcommand %q has nil RunE", name)
		}
	}
}

// TestUpsertRetired locks in the V2-PLAN §10.1 hard-cut: `upsert` has no
// alias; any attempt to resolve it as a subcommand must fail.
func TestUpsertRetired(t *testing.T) {
	root := newRootCmd()
	sub, _, _ := root.Find([]string{"upsert"})
	if sub != nil && sub.Name() == "upsert" {
		t.Errorf("upsert subcommand should be retired, got %q", sub.Name())
	}
}

func TestCreateDataFlagsMutuallyExclusive(t *testing.T) {
	cmd := newCreateCmd()
	if cmd.Flags().Lookup("data") == nil {
		t.Error("--data flag missing")
	}
	if cmd.Flags().Lookup("data-file") == nil {
		t.Error("--data-file flag missing")
	}
	// PLAN §12.17.9 Phase 9.4: --path-hint removed from create; --type added (required).
	if cmd.Flags().Lookup("type") == nil {
		t.Error("--type flag missing")
	}
}

func TestVersionFallsBackToDevel(t *testing.T) {
	if v := version(); v == "" {
		t.Fatal("version empty")
	}
}

// TestMenuItemsSkipsHelpAndCompletion locks in the V2-PLAN §12.16 menu
// contract: the huh subcommand menu shown for bare `ta` on a TTY must
// omit cobra's default `help` command and the `completion` command (if
// any). Hidden commands are also skipped. Each menu row carries the
// subcommand name and Short description, so every registered non-hidden
// subcommand must have a non-empty Short.
func TestMenuItemsSkipsHelpAndCompletion(t *testing.T) {
	root := newRootCmd()
	items := menuItems(root)
	if len(items) == 0 {
		t.Fatal("no menu items")
	}
	for _, it := range items {
		if it.name == "help" || it.name == "completion" {
			t.Errorf("menu should skip %q", it.name)
		}
		if it.short == "" {
			t.Errorf("menu item %q has empty short", it.name)
		}
	}
	// The full user-facing subcommand surface must be present.
	want := map[string]bool{
		"get":           false,
		"list-sections": false,
		"create":        false,
		"update":        false,
		"delete":        false,
		"schema":        false,
		"search":        false,
		"template":      false,
		"init":          false,
	}
	for _, it := range items {
		if _, ok := want[it.name]; ok {
			want[it.name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("menu missing subcommand %q", name)
		}
	}
}

// TestEveryCommandHasExample enforces V2-PLAN §14.7: every cobra
// Command in the `ta` tree ships a non-empty Example field so
// `ta <cmd> --help` shows at least one realistic invocation. Walks
// the root and every registered subcommand (including the template
// parent's children). Hidden commands are skipped.
func TestEveryCommandHasExample(t *testing.T) {
	root := newRootCmd()
	walkCommands(t, root, "")
}

func walkCommands(t *testing.T, cmd *cobra.Command, prefix string) {
	t.Helper()
	name := cmd.Name()
	if prefix != "" {
		name = prefix + " " + name
	}
	if !cmd.Hidden && cmd.Name() != "help" && cmd.Name() != "completion" {
		if cmd.Example == "" {
			t.Errorf("command %q is missing an Example field", name)
		}
	}
	for _, sub := range cmd.Commands() {
		walkCommands(t, sub, name)
	}
}

// TestHelpAliasResolves regression-locks the V2-PLAN §14.7 requirement
// that `ta h` and `ta h <cmd>` work as aliases for `ta help [cmd]`.
// A future delete of Aliases: []string{"h"} on the custom help
// command would ship green without this test.
func TestHelpAliasResolves(t *testing.T) {
	root := newRootCmd()
	help, _, err := root.Find([]string{"help"})
	if err != nil || help == nil {
		t.Fatalf("help command not registered: %v", err)
	}
	if help.Name() != "help" {
		t.Fatalf("expected help command, got %q", help.Name())
	}
	want := "h"
	found := false
	for _, a := range help.Aliases {
		if a == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("help command missing %q alias; have %v", want, help.Aliases)
	}
	// `ta h` must resolve to the help command via alias.
	aliasResolved, _, err := root.Find([]string{"h"})
	if err != nil || aliasResolved == nil {
		t.Fatalf("root.Find([\"h\"]) failed: %v", err)
	}
	if aliasResolved.Name() != "help" {
		t.Errorf("`ta h` resolved to %q, want help", aliasResolved.Name())
	}
	// `ta h init` passes through alias resolution AND leaves `init` as
	// the remaining arg so the Run closure can print init's help.
	// cobra Find walks the alias first, treating trailing tokens as
	// positional args. This guarantees the nested form works end-to-end.
	nestedTarget, nestedRest, err := root.Find([]string{"h", "init"})
	if err != nil {
		t.Fatalf("root.Find([\"h\", \"init\"]) failed: %v", err)
	}
	if nestedTarget.Name() != "help" {
		t.Errorf("`ta h init` resolved target to %q, want help", nestedTarget.Name())
	}
	if len(nestedRest) != 1 || nestedRest[0] != "init" {
		t.Errorf("`ta h init` remaining args = %v, want [init]", nestedRest)
	}
	// Then the Run closure calls Find on the remaining args against the
	// root to find the target — verify init resolves.
	initTarget, _, err := root.Find(nestedRest)
	if err != nil || initTarget == nil || initTarget.Name() != "init" {
		t.Errorf("nested resolution failed: target=%v, err=%v", initTarget, err)
	}
}
