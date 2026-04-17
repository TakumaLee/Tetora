package discord

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitForDiscord_ShortContentStaysOneChunk(t *testing.T) {
	got := splitForDiscord("hello world", 1990)
	if len(got) != 1 || got[0] != "hello world" {
		t.Errorf("expected single unsplit chunk, got %v", got)
	}
}

func TestSplitForDiscord_EmptyReturnsNil(t *testing.T) {
	got := splitForDiscord("", 1990)
	if got != nil {
		t.Errorf("expected nil for empty input (so SendLongMessage skips empty posts), got %v", got)
	}
}

func TestSplitForDiscord_SplitsAtParagraphBoundary(t *testing.T) {
	para1 := strings.Repeat("a", 900)
	para2 := strings.Repeat("b", 900)
	input := para1 + "\n\n" + para2
	got := splitForDiscord(input, 1000)
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(got))
	}
	if got[0] != para1 {
		t.Errorf("chunk 1 should be para1 without trailing newlines, got last 10 bytes: %q", got[0][len(got[0])-10:])
	}
	if got[1] != para2 {
		t.Errorf("chunk 2 should be para2, got first 10 bytes: %q", got[1][:10])
	}
}

func TestSplitForDiscord_FallsBackToLineBoundary(t *testing.T) {
	// No "\n\n" in content; splitter should use "\n".
	line := strings.Repeat("x", 500)
	input := line + "\n" + line + "\n" + line
	got := splitForDiscord(input, 800)
	if len(got) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(got))
	}
	for i, c := range got {
		if len(c) > 800 {
			t.Errorf("chunk %d exceeds budget: %d bytes", i, len(c))
		}
	}
}

func TestSplitForDiscord_FallsBackToWordBoundary(t *testing.T) {
	// No newlines — only spaces separate words.
	words := strings.Repeat("word ", 500) // ~2500 bytes
	got := splitForDiscord(strings.TrimSpace(words), 800)
	if len(got) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(got))
	}
	// No chunk should start or end with a partial word (chunk should not start with part of "word").
	for i, c := range got {
		if len(c) > 800 {
			t.Errorf("chunk %d exceeds budget: %d bytes", i, len(c))
		}
		// First char of a middle/last chunk should be the start of a word ('w').
		if i > 0 && c[0] != 'w' {
			t.Errorf("chunk %d should start at word boundary, starts with %q", i, string(c[0]))
		}
	}
}

func TestSplitForDiscord_RespectsRuneBoundary(t *testing.T) {
	// Build content with only CJK (3-byte runes) and no structural breaks so
	// splitter is forced into the rune-boundary fallback.
	cjk := strings.Repeat("中", 500) // 1500 bytes, 500 runes
	got := splitForDiscord(cjk, 700)
	if len(got) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(got))
	}
	for i, c := range got {
		if !utf8.ValidString(c) {
			t.Errorf("chunk %d produced invalid UTF-8", i)
		}
		if len(c) > 700 {
			t.Errorf("chunk %d exceeds budget: %d bytes", i, len(c))
		}
	}
	// Recombining should reproduce the original content byte-for-byte.
	if strings.Join(got, "") != cjk {
		t.Errorf("recombining chunks should equal original")
	}
}

func TestSplitForDiscord_VeryLongSingleToken(t *testing.T) {
	// A pathological string with no break points at all: one giant token.
	blob := strings.Repeat("a", 5000)
	got := splitForDiscord(blob, 1000)
	if len(got) < 5 {
		t.Errorf("expected ≥5 chunks for 5000-byte blob with budget 1000, got %d", len(got))
	}
	for i, c := range got {
		if len(c) > 1000 {
			t.Errorf("chunk %d exceeds budget: %d bytes", i, len(c))
		}
	}
}

func TestSplitForDiscord_RealisticRecapPreservesAllContent(t *testing.T) {
	// Realistic recap: several paragraphs of mixed CJK + ASCII.
	input := strings.Join([]string{
		"Goal: 驗證 recap 分段功能在真實情境下是否保留所有資訊。",
		"",
		"Task #1 completed: " + strings.Repeat("實作細節描述 ", 100),
		"",
		"Task #2 in progress: " + strings.Repeat("another long paragraph with details ", 50),
		"",
		"Next: 等待使用者確認。",
	}, "\n")
	got := splitForDiscord(input, 1990)
	// Recombining with "\n\n" between chunks (matches our prefix-free join) should
	// recover the same text modulo the boundary whitespace we trimmed.
	rejoined := strings.Join(got, "\n\n")
	// All original non-whitespace must survive.
	originalNoSpace := strings.ReplaceAll(strings.ReplaceAll(input, "\n", ""), " ", "")
	rejoinedNoSpace := strings.ReplaceAll(strings.ReplaceAll(rejoined, "\n", ""), " ", "")
	if originalNoSpace != rejoinedNoSpace {
		t.Errorf("content loss after split+rejoin")
	}
	for i, c := range got {
		if len(c) > 1990 {
			t.Errorf("chunk %d exceeds budget: %d bytes", i, len(c))
		}
	}
}
