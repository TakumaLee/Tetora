package session

import (
	"os"
	"strings"
	"testing"
)

func TestIsCorrection(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		// Chinese
		{"不對，應該這樣做", true},
		{"不是這樣", true},
		{"應該是用 GET 而不是 POST", true},
		{"你搞錯了", true},
		{"錯了，重做", true},
		{"不要這樣", true},
		{"我的意思是另一個方案", true},

		// Japanese
		{"違う、そっちじゃない", true},
		{"そうじゃなくて", true},

		// English
		{"No, that's wrong", true},
		{"No don't do that", true},
		{"Actually, I meant the other file", true},
		{"Actually that's not right", true},
		{"Wrong approach", true},
		{"That's not correct", true},
		{"I meant the production database", true},
		{"I said use GET", true},
		{"Instead, use the API endpoint", true},
		{"You misunderstood the requirement", true},

		// False positive guards — these should NOT be corrections
		{"No problem", false},
		{"No worries at all", false},
		{"Actually good job on this", false},
		{"可以幫我看看這個嗎", false},
		{"Please implement the feature", false},
		{"How does this work?", false},
		{"Good job!", false},
		{"", false},
		{"OK", false},
		{"幫我部署", false},
		{"新增一個功能", false},
	}

	for _, tt := range tests {
		got := IsCorrection(tt.msg)
		if got != tt.want {
			t.Errorf("IsCorrection(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestRecordCorrection(t *testing.T) {
	dir := t.TempDir()

	// First correction.
	err := RecordCorrection(dir, "ruri", "不對，應該用 GET", "我建議用 POST 方法...")
	if err != nil {
		t.Fatalf("RecordCorrection: %v", err)
	}

	// Check file exists.
	path := dir + "/rules/conversation-corrections.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corrections: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "不對，應該用 GET") {
		t.Error("correction not found in file")
	}
	if !strings.Contains(content, "POST 方法") {
		t.Error("assistant context not found")
	}

	// Dedup: same correction again.
	err = RecordCorrection(dir, "ruri", "不對，應該用 GET", "不同的回覆")
	if err != nil {
		t.Fatalf("RecordCorrection dedup: %v", err)
	}
	data2, _ := os.ReadFile(path)
	if strings.Count(string(data2), "不對，應該用 GET") != 1 {
		t.Error("dedup failed: correction appears more than once")
	}

	// Different correction.
	err = RecordCorrection(dir, "kokuyou", "Wrong, use interface{}", "I suggest using struct")
	if err != nil {
		t.Fatalf("RecordCorrection second: %v", err)
	}
	data3, _ := os.ReadFile(path)
	if !strings.Contains(string(data3), "Wrong, use interface{}") {
		t.Error("second correction not found")
	}
}

func TestRecordCorrection_EmptyWorkspace(t *testing.T) {
	err := RecordCorrection("", "ruri", "不對", "previous")
	if err != nil {
		t.Errorf("expected nil error for empty workspace, got: %v", err)
	}
}
