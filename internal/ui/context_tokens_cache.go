package ui

import (
	"os"
	"sync"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// contextTokensEntry is the context size read from one transcript, keyed by
// the (path, size, mtime) the read saw.
type contextTokensEntry struct {
	path   string
	size   int64
	mtime  time.Time
	tokens int
	known  bool
}

// contextTokensCache holds the current context size of each Claude session
// for the sidebar metadata line. refresh does all the file I/O and runs on the
// background status goroutine. get is a map read, so the render snapshot and
// View() never touch the disk. It has the shape of the tmux pane-info cache
// (RefreshPaneInfoCache / GetCachedPaneInfo).
type contextTokensCache struct {
	mu      sync.RWMutex
	entries map[string]contextTokensEntry // key: Instance.ID
	read    func(path string) (session.ContextTokensResult, error)
}

func newContextTokensCache() *contextTokensCache {
	return &contextTokensCache{
		entries: map[string]contextTokensEntry{},
		read:    session.ReadTranscriptContextTokens,
	}
}

// refresh re-reads the transcripts that changed since the last call. An
// unchanged file costs one stat. Sessions that are archived, not Claude, have
// no transcript on this host, or are no longer in instances get no entry.
// A nil cache does nothing, like get: a Home built without NewHome still runs
// backgroundStatusUpdate.
func (c *contextTokensCache) refresh(instances []*session.Instance) {
	if c == nil {
		return
	}
	c.mu.RLock()
	prev := c.entries
	c.mu.RUnlock()

	next := make(map[string]contextTokensEntry, len(prev))
	for _, inst := range instances {
		if inst == nil || inst.IsArchived() {
			continue
		}
		path := inst.ContextTokensTranscriptPath()
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		old, had := prev[inst.ID]
		samePath := had && old.path == path
		if samePath && old.size == info.Size() && old.mtime.Equal(info.ModTime()) {
			next[inst.ID] = old
			continue
		}

		entry := contextTokensEntry{path: path, size: info.Size(), mtime: info.ModTime()}
		res, err := c.read(path)
		switch {
		case err != nil:
			// Unknown: the row shows nothing rather than a stale number.
			// Drop the stat key, so the next tick reads again even when
			// the transcript stays idle.
			entry.size, entry.mtime = 0, time.Time{}
		case res.Status == session.ContextTokensFound:
			entry.tokens, entry.known = res.Tokens, true
		case res.Status == session.ContextTokensExhausted && samePath:
			// The newest record is older than the capped tail, so it is the
			// record the previous read of this file found.
			entry.tokens, entry.known = old.tokens, old.known
		}
		next[inst.ID] = entry
	}

	c.mu.Lock()
	c.entries = next
	c.mu.Unlock()
}

// clear drops every value, so a count cached before the setting went off
// never reappears when it comes back on. A nil cache does nothing.
func (c *contextTokensCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if len(c.entries) > 0 {
		c.entries = map[string]contextTokensEntry{}
	}
	c.mu.Unlock()
}

// get returns the context size of a session, and false when it is unknown.
func (c *contextTokensCache) get(instanceID string) (int, bool) {
	if c == nil {
		return 0, false
	}
	c.mu.RLock()
	entry, ok := c.entries[instanceID]
	c.mu.RUnlock()
	if !ok || !entry.known {
		return 0, false
	}
	return entry.tokens, true
}
