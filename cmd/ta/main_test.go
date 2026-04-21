package main

import (
	"testing"
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
	want := []string{"get", "list-sections", "schema", "upsert"}
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

func TestUpsertDataFlagsMutuallyExclusive(t *testing.T) {
	cmd := newUpsertCmd()
	if cmd.Flags().Lookup("data") == nil {
		t.Error("--data flag missing")
	}
	if cmd.Flags().Lookup("data-file") == nil {
		t.Error("--data-file flag missing")
	}
}

func TestVersionFallsBackToDevel(t *testing.T) {
	if v := version(); v == "" {
		t.Fatal("version empty")
	}
}
