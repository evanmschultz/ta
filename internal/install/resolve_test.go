package install

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/dotta"
	"github.com/evanmschultz/ta/internal/installconfig"
)

// TestResolveDestination_DefaultStrategyPreservesRelPath pins the no-flatten
// behavior: when FlattenStrategy is empty, the file's caller-prepared
// subtree-relative RelPath lands intact under projectRoot/Destination.
func TestResolveDestination_DefaultStrategyPreservesRelPath(t *testing.T) {
	sub := installconfig.Substrate{
		Destination: ".claude/skills",
	}
	file := dotta.FileMeta{
		Name:    "agent.md",
		RelPath: "qa-proof/agent.md",
	}
	projectRoot := "/abs/project"

	got, err := ResolveDestination(sub, file, projectRoot)
	if err != nil {
		t.Fatalf("ResolveDestination returned error: %v", err)
	}

	want := filepath.Clean("/abs/project/.claude/skills/qa-proof/agent.md")
	if got != want {
		t.Errorf("ResolveDestination = %q, want %q", got, want)
	}
}

// TestResolveDestination_ByBasenameDropsGroupDir pins the flatten path: when
// FlattenStrategy="by_basename", any middle directories in file.RelPath are
// dropped so the file lands directly under Destination. This is the
// claude_agents case (`agents/go/builder.md` → `<dest>/builder.md`).
func TestResolveDestination_ByBasenameDropsGroupDir(t *testing.T) {
	sub := installconfig.Substrate{
		Destination:     ".claude/agents",
		FlattenStrategy: FlattenStrategyByBasename,
	}
	file := dotta.FileMeta{
		Name:    "builder.md",
		RelPath: "go/builder.md",
	}
	projectRoot := "/abs/project"

	got, err := ResolveDestination(sub, file, projectRoot)
	if err != nil {
		t.Fatalf("ResolveDestination returned error: %v", err)
	}

	want := filepath.Clean("/abs/project/.claude/agents/builder.md")
	if got != want {
		t.Errorf("ResolveDestination = %q, want %q", got, want)
	}
}

// TestResolveDestination_RejectsAbsoluteSubstrateDestination pins the
// absolute-Destination guard: substrate Destination must be project-relative.
// filepath.Join would silently drop projectRoot if Destination is absolute;
// we reject loud instead.
func TestResolveDestination_RejectsAbsoluteSubstrateDestination(t *testing.T) {
	sub := installconfig.Substrate{
		Destination: "/etc/passwd",
	}
	file := dotta.FileMeta{
		Name:    "leak",
		RelPath: "leak",
	}
	projectRoot := "/abs/project"

	_, err := ResolveDestination(sub, file, projectRoot)
	if err == nil {
		t.Fatalf("ResolveDestination accepted absolute Destination; want error")
	}
	if !strings.Contains(err.Error(), "must be project-relative") {
		t.Errorf("error %q does not mention the absolute-Destination guard", err.Error())
	}
}

// TestResolveDestination_RejectsEmptyProjectRoot pins the missing-projectRoot
// guard: without a projectRoot the resolver cannot anchor a destination at
// all, so empty must surface as a loud error rather than producing a
// relative or root-relative path.
func TestResolveDestination_RejectsEmptyProjectRoot(t *testing.T) {
	sub := installconfig.Substrate{
		Destination: ".claude/agents",
	}
	file := dotta.FileMeta{
		Name:    "builder.md",
		RelPath: "go/builder.md",
	}

	_, err := ResolveDestination(sub, file, "")
	if err == nil {
		t.Fatalf("ResolveDestination accepted empty projectRoot; want error")
	}
	if !strings.Contains(err.Error(), "projectRoot") {
		t.Errorf("error %q does not mention projectRoot", err.Error())
	}
}

// TestResolveDestination_RejectsTraversalEscapingProjectRoot pins the
// defence-in-depth Clean check: a Destination or RelPath that climbs above
// projectRoot via ".." must be rejected rather than silently writing outside
// the target project tree.
func TestResolveDestination_RejectsTraversalEscapingProjectRoot(t *testing.T) {
	sub := installconfig.Substrate{
		Destination: "../../etc",
	}
	file := dotta.FileMeta{
		Name:    "passwd",
		RelPath: "passwd",
	}
	projectRoot := "/abs/project"

	_, err := ResolveDestination(sub, file, projectRoot)
	if err == nil {
		t.Fatalf("ResolveDestination accepted parent-traversal Destination; want error")
	}
	if !strings.Contains(err.Error(), "escapes projectRoot") {
		t.Errorf("error %q does not mention the escape guard", err.Error())
	}
}
