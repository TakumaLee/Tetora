package recap

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
)

// Record represents one `away_summary` line extracted from a Claude Code
// transcript JSONL file.
type Record struct {
	UUID      string `json:"uuid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	GitBranch string `json:"gitBranch"`
	Timestamp string `json:"timestamp"`
	Content   string `json:"content"`
}

// rawLine mirrors the subset of fields we read off the JSONL.
type rawLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	UUID      string `json:"uuid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	GitBranch string `json:"gitBranch"`
	Timestamp string `json:"timestamp"`
	Content   string `json:"content"`
}

// ReadAwaySummariesFrom opens path, seeks to startOffset, reads to EOF, and
// returns every `type:system subtype:away_summary` record plus the new offset
// (end of file). Malformed lines are skipped silently.
func ReadAwaySummariesFrom(path string, startOffset int64) ([]Record, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, startOffset, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, startOffset, err
	}
	size := info.Size()

	// File was truncated or rotated — start from beginning.
	if startOffset > size {
		startOffset = 0
	}

	if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
		return nil, startOffset, err
	}

	var records []Record
	scanner := bufio.NewScanner(f)
	// Transcript lines can be large (full message bodies). Raise the buffer.
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var r rawLine
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		if r.Type != "system" || r.Subtype != "away_summary" {
			continue
		}
		if r.UUID == "" || r.Content == "" {
			continue
		}
		records = append(records, Record{
			UUID:      r.UUID,
			SessionID: r.SessionID,
			CWD:       r.CWD,
			GitBranch: r.GitBranch,
			Timestamp: r.Timestamp,
			Content:   r.Content,
		})
	}
	if err := scanner.Err(); err != nil {
		return records, startOffset, err
	}
	return records, size, nil
}
