package session

import (
	"bytes"
	"encoding/json"
	"os"
)

// ContextTokensStatus says what ReadTranscriptContextTokens found.
type ContextTokensStatus int

const (
	// ContextTokensNone: the transcript holds no record that gives the
	// current context size (a new transcript, or a compaction with no turn
	// after it). Show nothing.
	ContextTokensNone ContextTokensStatus = iota
	// ContextTokensFound: Tokens is the current context size.
	ContextTokensFound
	// ContextTokensExhausted: the scan reached maxContextTokensScanBytes
	// before any such record. The newest record is older than the scanned
	// tail, so a value read earlier from the same file is still correct.
	ContextTokensExhausted
)

// ContextTokensResult is the current context size of a Claude transcript.
type ContextTokensResult struct {
	Tokens int
	Status ContextTokensStatus
}

// contextTokensChunkBytes is the backward read step. A var so tests can make
// chunk edges fall inside every line.
var contextTokensChunkBytes int64 = 64 * 1024

// maxContextTokensScanBytes bounds one backward scan. The newest usage record
// sat at most 161 KB from the end in 696 transcripts measured on 2026-09-30,
// and the largest single line was 1.9 MB, so 8 MB leaves room for a large
// tool result after the last usage. A var so tests can shrink it.
var maxContextTokensScanBytes int64 = 8 * 1024 * 1024

var (
	contextTokensUsageMarker   = []byte(`"usage"`)
	contextTokensCompactMarker = []byte(`"compact_boundary"`)
)

// contextTokensRecord is the part of a transcript record the reader needs.
type contextTokensRecord struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ReadTranscriptContextTokens returns the current context size of the Claude
// transcript at path, read backward from the end of the file.
//
// The size is input_tokens + cache_creation_input_tokens +
// cache_read_input_tokens of the newest main-chain assistant record whose sum
// is above zero, the same sum as SessionAnalytics.CurrentContextTokens. A
// compact_boundary record newer than that gives ContextTokensNone: every older
// usage record describes the context from before the compaction, and the
// record's compactMetadata.postTokens leaves out the system prompt, tools,
// memory and skills. The size stays unknown until the next turn.
func ReadTranscriptContextTokens(path string) (ContextTokensResult, error) {
	f, err := os.Open(path) // #nosec G304 -- callers pass a resolved transcript path
	if err != nil {
		return ContextTokensResult{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return ContextTokensResult{}, err
	}

	end := info.Size()
	var scanned int64
	// tail holds the pieces of a line that started before the chunk being
	// read, oldest piece first. Joining once per line keeps the copy cost
	// linear in the bytes read, even for a multi-MB line.
	var tail [][]byte
	for end > 0 {
		if scanned >= maxContextTokensScanBytes {
			return ContextTokensResult{Status: ContextTokensExhausted}, nil
		}
		n := min(contextTokensChunkBytes, end)
		start := end - n
		chunk := make([]byte, n)
		if _, err := f.ReadAt(chunk, start); err != nil {
			return ContextTokensResult{}, err
		}
		scanned += n
		end = start

		for {
			i := bytes.LastIndexByte(chunk, '\n')
			if i < 0 {
				tail = append([][]byte{chunk}, tail...)
				break
			}
			line := bytes.Join(append([][]byte{chunk[i+1:]}, tail...), nil)
			tail = nil
			chunk = chunk[:i]
			if res, ok := contextTokensFromLine(line); ok {
				return res, nil
			}
		}
	}
	// The start of the file ends the first line.
	if res, ok := contextTokensFromLine(bytes.Join(tail, nil)); ok {
		return res, nil
	}
	return ContextTokensResult{Status: ContextTokensNone}, nil
}

// contextTokensFromLine reports whether line settles the context size, and
// the result if it does.
func contextTokensFromLine(line []byte) (ContextTokensResult, bool) {
	if !bytes.Contains(line, contextTokensUsageMarker) && !bytes.Contains(line, contextTokensCompactMarker) {
		return ContextTokensResult{}, false
	}
	var rec contextTokensRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		// A torn last line, written while Claude Code appends, lands here.
		return ContextTokensResult{}, false
	}
	if rec.IsSidechain {
		return ContextTokensResult{}, false
	}
	switch {
	case rec.Type == "assistant":
		u := rec.Message.Usage
		if sum := u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens; sum > 0 {
			return ContextTokensResult{Tokens: sum, Status: ContextTokensFound}, true
		}
	case rec.Type == "system" && rec.Subtype == "compact_boundary":
		return ContextTokensResult{Status: ContextTokensNone}, true
	}
	return ContextTokensResult{}, false
}
