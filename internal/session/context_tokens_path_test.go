package session

import (
	"os"
	"path/filepath"
	"testing"
)

// TestContextTokensTranscriptPath_UsesTheInstanceConfigDir: a group that sets
// its own Claude config dir wins over CLAUDE_CONFIG_DIR, and the transcript
// lives under the group's dir. GetJSONLPath resolves against the process-wide
// dir and misses it, which is why the sidebar does not use GetJSONLPath.
func TestContextTokensTranscriptPath_UsesTheInstanceConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude-env"))
	groupDir := filepath.Join(home, ".claude-team-a")
	cfg := &UserConfig{Groups: map[string]GroupSettings{
		"team-a": {Claude: GroupClaudeSettings{ConfigDir: groupDir}},
	}}
	withConfig(t, cfg)

	projectPath := t.TempDir()
	sessionID := "22222222-3333-4444-5555-666666666666"
	projDir := claudeProjectDirForTest(t, groupDir, projectPath)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(projDir, sessionID+".jsonl")
	if err := os.WriteFile(want, []byte(jsonlBody(usageAssistantLine("m", 1, 1, 1, 1))), 0o600); err != nil {
		t.Fatal(err)
	}

	inst := NewInstanceWithTool("grouped", projectPath, "claude")
	inst.GroupPath = "team-a"
	inst.ClaudeSessionID = sessionID

	if got := inst.ContextTokensTranscriptPath(); got != want {
		t.Errorf("ContextTokensTranscriptPath() = %q, want %q", got, want)
	}
	if got := inst.GetJSONLPath(); got == want {
		t.Errorf("GetJSONLPath() found the group transcript; this test no longer shows why the sidebar needs its own resolver")
	}
}

func TestContextTokensTranscriptPath_EmptyWithoutAClaudeTranscript(t *testing.T) {
	local, _, sessionID, projectPath := remoteBoundaryFixture(t)

	noID := NewInstanceWithTool("no-id", projectPath, "claude")
	if got := noID.ContextTokensTranscriptPath(); got != "" {
		t.Errorf("no Claude session id: got %q, want \"\"", got)
	}

	codex := NewInstanceWithTool("codex", projectPath, "codex")
	codex.ClaudeSessionID = sessionID
	if got := codex.ContextTokensTranscriptPath(); got != "" {
		t.Errorf("codex tool: got %q, want \"\"", got)
	}

	if got := local.ContextTokensTranscriptPath(); got == "" {
		t.Error("local Claude session: got \"\", want its transcript")
	}
}
