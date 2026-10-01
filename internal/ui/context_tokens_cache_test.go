package ui

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// contextTokensFixture isolates the Claude config dir and returns a helper
// that writes a transcript for a session id under projectPath.
func contextTokensFixture(t *testing.T) (projectPath string, write func(sessionID, body string) string) {
	t.Helper()
	home := t.TempDir()
	configDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	projectPath = t.TempDir()
	resolved := projectPath
	if r, err := filepath.EvalSymlinks(projectPath); err == nil {
		resolved = r
	}
	projDir := filepath.Join(configDir, "projects", session.ConvertToClaudeDirName(resolved))
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write = func(sessionID, body string) string {
		t.Helper()
		path := filepath.Join(projDir, sessionID+".jsonl")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	return projectPath, write
}

// scriptedRead is a fake reader: it returns the next scripted result and
// counts the calls.
type scriptedRead struct {
	mu      sync.Mutex
	results []session.ContextTokensResult
	errs    []error
	calls   int
}

func (s *scriptedRead) read(string) (session.ContextTokensResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := min(s.calls, len(s.results)-1)
	s.calls++
	var err error
	if i < len(s.errs) {
		err = s.errs[i]
	}
	return s.results[i], err
}

func found(n int) session.ContextTokensResult {
	return session.ContextTokensResult{Tokens: n, Status: session.ContextTokensFound}
}

// bumpFile appends a line and moves the mtime forward, so the (size, mtime)
// gate sees a change even on a filesystem with coarse timestamps.
func bumpFile(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"type\":\"mode\"}\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// claudeInstance builds a Claude session. The first argument of
// NewInstanceWithTool is the title: the cache keys on the generated inst.ID.
func claudeInstance(title, projectPath, sessionID string) *session.Instance {
	inst := session.NewInstanceWithTool(title, projectPath, "claude")
	inst.ClaudeSessionID = sessionID
	return inst
}

func TestContextTokensCache_UnchangedFileIsNotReadAgain(t *testing.T) {
	projectPath, write := contextTokensFixture(t)
	path := write("aaaaaaaa-0000-0000-0000-000000000001", "{}\n")
	inst := claudeInstance("a", projectPath, "aaaaaaaa-0000-0000-0000-000000000001")

	fake := &scriptedRead{results: []session.ContextTokensResult{found(100), found(200)}}
	c := newContextTokensCache()
	c.read = fake.read

	c.refresh([]*session.Instance{inst})
	c.refresh([]*session.Instance{inst})
	if fake.calls != 1 {
		t.Fatalf("read calls after two refreshes of an unchanged file = %d, want 1", fake.calls)
	}
	if n, ok := c.get(inst.ID); !ok || n != 100 {
		t.Fatalf("get = (%d, %v), want (100, true)", n, ok)
	}

	bumpFile(t, path)
	c.refresh([]*session.Instance{inst})
	if fake.calls != 2 {
		t.Fatalf("read calls after the file changed = %d, want 2", fake.calls)
	}
	if n, ok := c.get(inst.ID); !ok || n != 200 {
		t.Fatalf("get = (%d, %v), want (200, true)", n, ok)
	}
}

// TestContextTokensCache_ExhaustedKeepsTheValueOfTheSamePath: the scan found
// nothing in the capped tail, so the newest record is the one read before.
func TestContextTokensCache_ExhaustedKeepsTheValueOfTheSamePath(t *testing.T) {
	projectPath, write := contextTokensFixture(t)
	path := write("aaaaaaaa-0000-0000-0000-000000000002", "{}\n")
	inst := claudeInstance("a", projectPath, "aaaaaaaa-0000-0000-0000-000000000002")

	fake := &scriptedRead{results: []session.ContextTokensResult{
		found(115200),
		{Status: session.ContextTokensExhausted},
	}}
	c := newContextTokensCache()
	c.read = fake.read

	c.refresh([]*session.Instance{inst})
	bumpFile(t, path)
	c.refresh([]*session.Instance{inst})
	if fake.calls != 2 {
		t.Fatalf("read calls = %d, want 2", fake.calls)
	}
	if n, ok := c.get(inst.ID); !ok || n != 115200 {
		t.Fatalf("get = (%d, %v), want (115200, true)", n, ok)
	}
}

// TestContextTokensCache_NewPathDropsTheOldValue: when the session id is
// rebound to a new transcript that has no usage yet, the old value goes.
func TestContextTokensCache_NewPathDropsTheOldValue(t *testing.T) {
	for _, second := range []session.ContextTokensResult{
		{Status: session.ContextTokensNone},
		{Status: session.ContextTokensExhausted},
	} {
		projectPath, write := contextTokensFixture(t)
		write("aaaaaaaa-0000-0000-0000-000000000003", "{}\n")
		write("bbbbbbbb-0000-0000-0000-000000000003", "{}\n")
		inst := claudeInstance("a", projectPath, "aaaaaaaa-0000-0000-0000-000000000003")

		fake := &scriptedRead{results: []session.ContextTokensResult{found(900000), second}}
		c := newContextTokensCache()
		c.read = fake.read

		c.refresh([]*session.Instance{inst})
		inst.ClaudeSessionID = "bbbbbbbb-0000-0000-0000-000000000003"
		c.refresh([]*session.Instance{inst})
		if n, ok := c.get(inst.ID); ok {
			t.Errorf("second read %v: get = (%d, true), want unknown", second.Status, n)
		}
	}
}

// TestContextTokensCache_NoneOnTheSamePathIsUnknown is the /compact case: the
// transcript keeps its path, a compact_boundary lands after the last usage,
// and the reader gives None. The row drops the count from before the
// compaction, where Exhausted on the same path would keep it.
func TestContextTokensCache_NoneOnTheSamePathIsUnknown(t *testing.T) {
	projectPath, write := contextTokensFixture(t)
	path := write("aaaaaaaa-0000-0000-0000-000000000007", "{}\n")
	inst := claudeInstance("a", projectPath, "aaaaaaaa-0000-0000-0000-000000000007")

	fake := &scriptedRead{results: []session.ContextTokensResult{
		found(38300),
		{Status: session.ContextTokensNone},
	}}
	c := newContextTokensCache()
	c.read = fake.read

	c.refresh([]*session.Instance{inst})
	if n, ok := c.get(inst.ID); !ok || n != 38300 {
		t.Fatalf("get before the compaction = (%d, %v), want (38300, true)", n, ok)
	}
	bumpFile(t, path)
	c.refresh([]*session.Instance{inst})
	if fake.calls != 2 {
		t.Fatalf("read calls = %d, want 2", fake.calls)
	}
	if n, ok := c.get(inst.ID); ok {
		t.Errorf("get after None on the same path = (%d, true), want unknown", n)
	}
}

func TestContextTokensCache_ReadErrorIsUnknown(t *testing.T) {
	projectPath, write := contextTokensFixture(t)
	path := write("aaaaaaaa-0000-0000-0000-000000000004", "{}\n")
	inst := claudeInstance("a", projectPath, "aaaaaaaa-0000-0000-0000-000000000004")

	fake := &scriptedRead{
		results: []session.ContextTokensResult{found(100), {}},
		errs:    []error{nil, errors.New("read failed")},
	}
	c := newContextTokensCache()
	c.read = fake.read

	c.refresh([]*session.Instance{inst})
	bumpFile(t, path)
	c.refresh([]*session.Instance{inst})
	if n, ok := c.get(inst.ID); ok {
		t.Errorf("get after a read error = (%d, true), want unknown", n)
	}
}

// TestContextTokensCache_ReadErrorIsRetried: a transient read error on an
// idle transcript must not stick until the file changes again.
func TestContextTokensCache_ReadErrorIsRetried(t *testing.T) {
	projectPath, write := contextTokensFixture(t)
	write("aaaaaaaa-0000-0000-0000-000000000005", "{}\n")
	inst := claudeInstance("a", projectPath, "aaaaaaaa-0000-0000-0000-000000000005")

	fake := &scriptedRead{
		results: []session.ContextTokensResult{{}, found(300)},
		errs:    []error{errors.New("read failed"), nil},
	}
	c := newContextTokensCache()
	c.read = fake.read

	c.refresh([]*session.Instance{inst})
	c.refresh([]*session.Instance{inst})
	if fake.calls != 2 {
		t.Fatalf("read calls after an error on an unchanged file = %d, want 2", fake.calls)
	}
	if n, ok := c.get(inst.ID); !ok || n != 300 {
		t.Fatalf("get = (%d, %v), want (300, true)", n, ok)
	}
}

// TestContextTokensCache_OnlyLiveClaudeRowsHaveEntries: archived, non-Claude
// and transcript-less sessions get no entry, and a session that leaves the
// list loses its entry.
func TestContextTokensCache_OnlyLiveClaudeRowsHaveEntries(t *testing.T) {
	projectPath, write := contextTokensFixture(t)
	write("aaaaaaaa-0000-0000-0000-000000000005", "{}\n")

	live := claudeInstance("live", projectPath, "aaaaaaaa-0000-0000-0000-000000000005")
	archived := claudeInstance("archived", projectPath, "aaaaaaaa-0000-0000-0000-000000000005")
	archived.ArchivedAt = time.Now()
	codex := session.NewInstanceWithTool("codex", projectPath, "codex")
	codex.ClaudeSessionID = "aaaaaaaa-0000-0000-0000-000000000005"
	noFile := claudeInstance("nofile", projectPath, "cccccccc-0000-0000-0000-000000000005")

	fake := &scriptedRead{results: []session.ContextTokensResult{found(100)}}
	c := newContextTokensCache()
	c.read = fake.read

	c.refresh([]*session.Instance{live, archived, codex, noFile})
	if _, ok := c.get(live.ID); !ok {
		t.Error("live Claude session has no value")
	}
	for _, other := range []*session.Instance{archived, codex, noFile} {
		if n, ok := c.get(other.ID); ok {
			t.Errorf("%s: get = (%d, true), want unknown", other.Title, n)
		}
	}

	c.refresh(nil)
	if n, ok := c.get(live.ID); ok {
		t.Errorf("session left the list: get = (%d, true), want unknown", n)
	}
}

func TestContextTokensCache_NilCacheIsUnknown(t *testing.T) {
	var c *contextTokensCache
	if n, ok := c.get("any"); ok {
		t.Errorf("nil cache: get = (%d, true), want unknown", n)
	}
}

// TestContextTokensCache_RefreshAndGetInParallel is for `go test -race`:
// refresh runs on the background status goroutine while the snapshot
// builder calls get.
func TestContextTokensCache_RefreshAndGetInParallel(t *testing.T) {
	projectPath, write := contextTokensFixture(t)
	path := write("aaaaaaaa-0000-0000-0000-000000000006", "{}\n")
	inst := claudeInstance("a", projectPath, "aaaaaaaa-0000-0000-0000-000000000006")

	fake := &scriptedRead{results: []session.ContextTokensResult{found(1)}}
	c := newContextTokensCache()
	c.read = fake.read

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		// Only the mtime moves: t.Fatal (inside bumpFile) must not run
		// off the test goroutine.
		for i := 0; i < 50; i++ {
			ts := time.Now().Add(time.Duration(i+1) * time.Second)
			_ = os.Chtimes(path, ts, ts)
			c.refresh([]*session.Instance{inst})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			c.get(inst.ID)
		}
	}()
	wg.Wait()
}
