package session

// ContextTokensTranscriptPath returns the transcript that the sidebar reads
// the current context size from, or "" when this host has none for the
// instance. It is door 19 in remote_transcript_boundary.go.
//
// It resolves against the instance's own Claude config dir, as recall does,
// because GetJSONLPath uses the process-wide dir and misses a transcript
// under an account, conductor or group config dir. It copies the fields that
// UpdateClaudeSession writes under i.mu, so a background goroutine may call it.
func (i *Instance) ContextTokensTranscriptPath() string {
	if !i.TranscriptIsResolvableLocally() {
		return ""
	}
	i.mu.RLock()
	tool, sessionID, projectPath := i.Tool, i.ClaudeSessionID, i.ProjectPath
	i.mu.RUnlock()
	if !IsClaudeCompatible(tool) || sessionID == "" {
		return ""
	}
	return ResolveClaudeTranscriptPath(GetClaudeConfigDirForInstance(i), projectPath, sessionID)
}
