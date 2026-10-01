package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// TestBackgroundStatusUpdateCarriesContextTokensIntoTheSnapshot runs the
// status tick itself rather than the cache alone: the snapshot of a pass
// carries the count that the pass's own refresh read, and a pass with the
// setting off clears the cache, so a count cached before the setting went
// off cannot come back with it.
func TestBackgroundStatusUpdateCarriesContextTokensIntoTheSnapshot(t *testing.T) {
	projectPath, write := contextTokensFixture(t)
	const sessionID = "aaaaaaaa-0000-0000-0000-000000000070"
	write(sessionID, `{"type":"assistant","message":{"usage":{"input_tokens":2,"output_tokens":7,"cache_creation_input_tokens":385,"cache_read_input_tokens":437923}}}`+"\n")
	inst := claudeInstance("tick", projectPath, sessionID)
	// No tmux session, so the status poll of the pass leaves Tool alone.
	// With one, the stub below makes DetectTool report "shell", and the row
	// stops being a Claude row.
	inst.SetTmuxSessionForTest(nil)

	// A tmux that answers every call with nothing, so the pass gets past its
	// tmux.IsServerAlive early return without touching a real server.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := &Home{embeddedLayout: true, contextTokens: newContextTokensCache()}
	h.instances = []*session.Instance{inst}

	h.backgroundStatusUpdate()
	state, ok := h.getSessionRenderSnapshot()[inst.ID]
	if !ok {
		t.Fatal("the pass published no snapshot row: it returned before refreshSessionRenderSnapshot")
	}
	if !state.contextTokensKnown || state.contextTokens != 438310 {
		t.Fatalf("snapshot after a pass = (%d, %v), want (438310, true): the pass did not refresh the cache",
			state.contextTokens, state.contextTokensKnown)
	}

	h.hideContextTokens.Store(true)
	h.backgroundStatusUpdate()
	if state := h.getSessionRenderSnapshot()[inst.ID]; state.contextTokensKnown {
		t.Fatalf("snapshot after a pass with the setting off = (%d, true), want unknown", state.contextTokens)
	}
	if n, ok := h.contextTokens.get(inst.ID); ok {
		t.Fatalf("cache after a pass with the setting off = (%d, true), want unknown: the pass did not clear it", n)
	}
}
