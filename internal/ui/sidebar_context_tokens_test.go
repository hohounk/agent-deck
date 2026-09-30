package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// knownContextTokensCache returns a cache that already holds one known value,
// so a render test needs no transcript on disk.
func knownContextTokensCache(instanceID string, tokens int) *contextTokensCache {
	c := newContextTokensCache()
	c.entries[instanceID] = contextTokensEntry{tokens: tokens, known: true}
	return c
}

// renderEmbeddedRowLines renders one session row of the embedded sidebar
// from a hand-built snapshot and returns its lines without ANSI codes.
func renderEmbeddedRowLines(t *testing.T, home *Home, inst *session.Instance, state sessionRenderState, width int) []string {
	t.Helper()
	var b strings.Builder
	item := session.Item{Type: session.ItemTypeSession, Session: inst, Level: 1}
	home.renderSessionItem(&b, item, false, map[string]sessionRenderState{inst.ID: state}, width)
	return strings.Split(strings.TrimSuffix(ansi.Strip(b.String()), "\n"), "\n")
}

// embeddedMetadataParts splits the last metadata line of a row, the one with
// the closing elbow, into its " · " parts.
func embeddedMetadataParts(t *testing.T, lines []string) []string {
	t.Helper()
	_, text, ok := strings.Cut(lines[len(lines)-1], "╰ ")
	if !ok {
		t.Fatalf("row has no closing metadata line: %q", lines)
	}
	return strings.Split(strings.TrimSpace(text), " · ")
}

// contextTokensRowFixture is one Claude row in the embedded layout at the
// default density, with a pane title that the metadata line shows.
func contextTokensRowFixture(t *testing.T) (*Home, *session.Instance, sessionRenderState) {
	t.Helper()
	home, inst, _ := armHomeWithOneSession(t)
	home.embeddedLayout = true
	home.compactSidebar = false
	home.sidebarDensity = session.SidebarDensityCompact
	state := sessionRenderState{
		status:             session.StatusIdle,
		tool:               "claude",
		title:              "focused-session",
		paneTitle:          "fixing the parser",
		contextTokens:      115200,
		contextTokensKnown: true,
	}
	return home, inst, state
}

func TestSidebarContextTokensSettingIsReadAtStartupAndOnSave(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		setIsolatedAgentDeckDir(t)
		home := NewHome()
		if home.contextTokens == nil {
			t.Fatal("NewHome did not create the context tokens cache")
		}
		if home.hideContextTokens.Load() {
			t.Fatal("unset sidebar_context_tokens hid the count")
		}
	})
	t.Run("false in config.toml", func(t *testing.T) {
		setIsolatedAgentDeckDir(t)
		off := false
		if err := session.SaveUserConfig(&session.UserConfig{UI: session.UISettings{SidebarContextTokens: &off}}); err != nil {
			t.Fatal(err)
		}
		session.ClearUserConfigCache()
		home := NewHome()
		if !home.hideContextTokens.Load() {
			t.Fatal("sidebar_context_tokens = false did not hide the count at startup")
		}
		home.applySavedLayoutSettings(session.UISettings{})
		if home.hideContextTokens.Load() {
			t.Fatal("a save with the key unset did not show the count again")
		}
		home.applySavedLayoutSettings(session.UISettings{SidebarContextTokens: &off})
		if !home.hideContextTokens.Load() {
			t.Fatal("a save with sidebar_context_tokens = false did not hide the count")
		}
	})
}

// TestSidebarContextTokensRefreshRunsOnlyOnTheStatusTick is a source guard:
// the cache refresh stats and reads transcripts, so it runs on the background
// status tick right before the render snapshot, behind the setting, and
// nowhere else.
func TestSidebarContextTokensRefreshRunsOnlyOnTheStatusTick(t *testing.T) {
	src, err := os.ReadFile("home.go")
	if err != nil {
		t.Fatalf("read home.go: %v", err)
	}
	body := funcBody(t, string(src), "func (h *Home) backgroundStatusUpdate() {")
	want := "\tif !h.hideContextTokens.Load() {\n" +
		"\t\th.contextTokens.refresh(instances)\n" +
		"\t}\n" +
		"\th.refreshSessionRenderSnapshot(instances)\n"
	if !strings.Contains(body, want) {
		t.Fatalf("backgroundStatusUpdate must refresh the context tokens cache behind "+
			"hideContextTokens, immediately before its render snapshot:\n%s", want)
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		text, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		calls += strings.Count(string(text), "contextTokens.refresh(")
	}
	if calls != 1 {
		t.Fatalf("contextTokens.refresh( appears %d times in internal/ui, want 1 (backgroundStatusUpdate)", calls)
	}
}

// TestNilContextTokensCacheRefreshIsANoOp: tests build Home literals without
// the cache and still run backgroundStatusUpdate, whose recover would swallow
// a nil dereference and skip the render snapshot of that tick.
func TestNilContextTokensCacheRefreshIsANoOp(t *testing.T) {
	var c *contextTokensCache
	inst := session.NewInstanceWithTool("nil-cache", t.TempDir(), "claude")
	c.refresh([]*session.Instance{inst})
	if n, ok := c.get(inst.ID); ok {
		t.Fatalf("nil cache get = (%d, true), want unknown", n)
	}
}

func TestRenderSnapshotCarriesContextTokens(t *testing.T) {
	home, inst, _ := armHomeWithOneSession(t)
	home.contextTokens = knownContextTokensCache(inst.ID, 115200)
	home.refreshSessionRenderSnapshot([]*session.Instance{inst})
	state := home.getSessionRenderSnapshot()[inst.ID]
	if !state.contextTokensKnown || state.contextTokens != 115200 {
		t.Fatalf("snapshot context tokens = (%d, %v), want (115200, true)", state.contextTokens, state.contextTokensKnown)
	}

	// A Home built without NewHome has no cache: the value is unknown.
	home.contextTokens = nil
	home.refreshSessionRenderSnapshot([]*session.Instance{inst})
	state = home.getSessionRenderSnapshot()[inst.ID]
	if state.contextTokensKnown || state.contextTokens != 0 {
		t.Fatalf("snapshot with no cache = (%d, %v), want (0, false)", state.contextTokens, state.contextTokensKnown)
	}
}

func TestEmbeddedRowShowsContextTokensBeforePaneTitle(t *testing.T) {
	home, inst, state := contextTokensRowFixture(t)
	state.substate = session.SubstateBackgroundWork
	home.resumingSessions[inst.ID] = time.Now()

	lines := renderEmbeddedRowLines(t, home, inst, state, 100)
	if len(lines) != 2 {
		t.Fatalf("row rendered %d lines, want 2: %q", len(lines), lines)
	}
	want := []string{"idle", "claude", string(session.SubstateBackgroundWork), "restarting…", "115.2K", "fixing the parser"}
	if got := embeddedMetadataParts(t, lines); !slices.Equal(got, want) {
		t.Fatalf("metadata parts = %q, want %q", got, want)
	}
}

func TestEmbeddedRowAddsNoContextTokensPart(t *testing.T) {
	cases := []struct {
		name  string
		setup func(home *Home, state *sessionRenderState)
	}{
		{name: "unknown value", setup: func(_ *Home, state *sessionRenderState) {
			state.contextTokens, state.contextTokensKnown = 0, false
		}},
		{name: "setting off", setup: func(home *Home, _ *sessionRenderState) {
			home.hideContextTokens.Store(true)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, inst, state := contextTokensRowFixture(t)
			tc.setup(home, &state)
			lines := renderEmbeddedRowLines(t, home, inst, state, 100)
			want := []string{"idle", "claude", "fixing the parser"}
			if got := embeddedMetadataParts(t, lines); !slices.Equal(got, want) {
				t.Fatalf("metadata parts = %q, want %q", got, want)
			}
		})
	}
}

func TestEmbeddedRowAtMinimalDensityShowsNoContextTokens(t *testing.T) {
	home, inst, state := contextTokensRowFixture(t)
	home.sidebarDensity = session.SidebarDensityMinimal

	lines := renderEmbeddedRowLines(t, home, inst, state, 100)
	if len(lines) != 1 {
		t.Fatalf("minimal row rendered %d lines, want 1: %q", len(lines), lines)
	}
	if strings.Contains(lines[0], "115.2K") {
		t.Fatalf("minimal row shows the context count: %q", lines[0])
	}
}
