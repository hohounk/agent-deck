package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// compactBoundaryLine builds the system record Claude Code writes at a
// compaction. postTokens <= 0 leaves the field out, as older versions do.
func compactBoundaryLine(postTokens int) string {
	meta := `{"trigger":"manual","preTokens":188127}`
	if postTokens > 0 {
		meta = fmt.Sprintf(`{"trigger":"manual","preTokens":188127,"postTokens":%d}`, postTokens)
	}
	return `{"type":"system","subtype":"compact_boundary","content":"Conversation compacted","compactMetadata":` + meta + `}`
}

// writeContextTokensTranscript writes body verbatim, so a case controls the
// trailing newline itself.
func writeContextTokensTranscript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func jsonlBody(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func TestReadTranscriptContextTokens(t *testing.T) {
	older := usageAssistantLine("claude-opus-4-7", 10, 5, 2000, 100000) // 102010
	newer := usageAssistantLine("claude-opus-4-7", 2, 7, 385, 437923)   // 438310
	bigToolResult := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 300) + ` usage "}}`

	cases := []struct {
		name string
		body string
		want ContextTokensResult
	}{
		{"last line is an assistant record with usage",
			jsonlBody(older, newer),
			ContextTokensResult{Tokens: 438310, Status: ContextTokensFound}},
		{"metadata records after the usage",
			jsonlBody(newer,
				`{"type":"last-prompt","lastPrompt":"hi"}`,
				`{"type":"ai-title","aiTitle":"t"}`,
				`{"type":"mode","mode":"default"}`),
			ContextTokensResult{Tokens: 438310, Status: ContextTokensFound}},
		// postTokens is not the context size: it leaves out the system
		// prompt, tools, memory and skills. The size is unknown until the
		// next turn writes a usage record.
		{"compact_boundary with postTokens after the usage",
			jsonlBody(newer, compactBoundaryLine(13580), `{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"summary"}}`),
			ContextTokensResult{Status: ContextTokensNone}},
		{"compact_boundary without postTokens after the usage",
			jsonlBody(newer, compactBoundaryLine(0)),
			ContextTokensResult{Status: ContextTokensNone}},
		{"usage after a compact_boundary",
			jsonlBody(older, compactBoundaryLine(13580), newer),
			ContextTokensResult{Tokens: 438310, Status: ContextTokensFound}},
		// The compact_boundary prefilter matches any line that carries the
		// quoted word, so only a system record of that subtype may end the
		// scan with None.
		{"user record quoting compact_boundary after the usage",
			jsonlBody(newer, `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"compact_boundary"}]}}`),
			ContextTokensResult{Tokens: 438310, Status: ContextTokensFound}},
		{"sidechain record is the newest usage",
			jsonlBody(older, `{"type":"assistant","isSidechain":true,"message":{"usage":{"input_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":999}}}`),
			ContextTokensResult{Tokens: 102010, Status: ContextTokensFound}},
		{"zero usage sum is the newest record",
			jsonlBody(older, usageAssistantLine("<synthetic>", 0, 0, 0, 0)),
			ContextTokensResult{Tokens: 102010, Status: ContextTokensFound}},
		{"line longer than one chunk after the usage",
			jsonlBody(newer, bigToolResult),
			ContextTokensResult{Tokens: 438310, Status: ContextTokensFound}},
		{"torn last line",
			jsonlBody(older) + newer[:len(newer)/2],
			ContextTokensResult{Tokens: 102010, Status: ContextTokensFound}},
		{"usage on the first line of the file",
			jsonlBody(newer, `{"type":"mode","mode":"default"}`),
			ContextTokensResult{Tokens: 438310, Status: ContextTokensFound}},
		{"no trailing newline",
			older + "\n" + newer,
			ContextTokensResult{Tokens: 438310, Status: ContextTokensFound}},
		{"file without usage",
			jsonlBody(`{"type":"user","message":{"role":"user","content":"hi"}}`, `{"type":"mode","mode":"default"}`),
			ContextTokensResult{Status: ContextTokensNone}},
		{"empty file",
			"",
			ContextTokensResult{Status: ContextTokensNone}},
	}

	// The answer must not depend on where chunk edges fall: run every case
	// with the production chunk and with a 7-byte chunk that splits every
	// line many times.
	for _, chunk := range []int64{contextTokensChunkBytes, 7} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("chunk=%d/%s", chunk, tc.name), func(t *testing.T) {
				orig := contextTokensChunkBytes
				contextTokensChunkBytes = chunk
				defer func() { contextTokensChunkBytes = orig }()

				got, err := ReadTranscriptContextTokens(writeContextTokensTranscript(t, tc.body))
				if err != nil {
					t.Fatalf("ReadTranscriptContextTokens: %v", err)
				}
				if got != tc.want {
					t.Errorf("got %+v, want %+v", got, tc.want)
				}
			})
		}
	}
}

// TestReadTranscriptContextTokens_CapGivesExhausted: when the cap is reached
// before any usage or compact record, the reader stops and says so, rather
// than reporting "none" (which the cache would show as blank).
func TestReadTranscriptContextTokens_CapGivesExhausted(t *testing.T) {
	origChunk, origCap := contextTokensChunkBytes, maxContextTokensScanBytes
	contextTokensChunkBytes, maxContextTokensScanBytes = 16, 64
	defer func() { contextTokensChunkBytes, maxContextTokensScanBytes = origChunk, origCap }()

	body := jsonlBody(
		usageAssistantLine("claude-opus-4-7", 2, 7, 385, 437923),
		`{"type":"user","message":{"role":"user","content":"`+strings.Repeat("y", 200)+`"}}`,
	)
	got, err := ReadTranscriptContextTokens(writeContextTokensTranscript(t, body))
	if err != nil {
		t.Fatalf("ReadTranscriptContextTokens: %v", err)
	}
	if want := (ContextTokensResult{Status: ContextTokensExhausted}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestReadTranscriptContextTokens_MissingFileIsAnError(t *testing.T) {
	_, err := ReadTranscriptContextTokens(filepath.Join(t.TempDir(), "absent.jsonl"))
	if err == nil {
		t.Fatal("want an error for a missing file, got nil")
	}
}
