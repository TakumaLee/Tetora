package nlp

import (
	"math"
	"testing"
)

func TestAnalyze_Positive(t *testing.T) {
	tests := []struct {
		text     string
		minScore float64
	}{
		{"I'm so happy today!", 0.5},
		{"This is great, thanks!", 0.5},
		{"I love this, it's awesome", 0.5},
		{"Perfect, excellent work!", 0.5},
		{"Wonderful and amazing results", 0.5},
	}
	for _, tt := range tests {
		r := Analyze(tt.text)
		if r.Score < tt.minScore {
			t.Errorf("Analyze(%q) score=%f, want >= %f (keywords=%v)", tt.text, r.Score, tt.minScore, r.Keywords)
		}
		if len(r.Keywords) == 0 {
			t.Errorf("Analyze(%q) expected keywords, got none", tt.text)
		}
	}
}

func TestAnalyze_Negative(t *testing.T) {
	tests := []struct {
		text     string
		maxScore float64
	}{
		{"I'm so sad and disappointed", -0.5},
		{"This is terrible and awful", -0.5},
		{"I hate this, it's the worst", -0.5},
		{"I'm frustrated and annoyed", -0.5},
		{"Bad, really bad experience", -0.5},
	}
	for _, tt := range tests {
		r := Analyze(tt.text)
		if r.Score > tt.maxScore {
			t.Errorf("Analyze(%q) score=%f, want <= %f (keywords=%v)", tt.text, r.Score, tt.maxScore, r.Keywords)
		}
		if len(r.Keywords) == 0 {
			t.Errorf("Analyze(%q) expected keywords, got none", tt.text)
		}
	}
}

func TestAnalyze_Mixed(t *testing.T) {
	// Mix of positive and negative should result in moderate score.
	r := Analyze("I love this but it's also terrible")
	if r.Score < -1 || r.Score > 1 {
		t.Errorf("mixed sentiment score out of range: %f", r.Score)
	}
	if len(r.Keywords) < 2 {
		t.Errorf("mixed sentiment expected multiple keywords, got %d", len(r.Keywords))
	}

	// Equal positive and negative should be near zero.
	r2 := Analyze("happy and sad")
	if math.Abs(r2.Score) > 0.01 {
		t.Errorf("equal positive/negative should be ~0, got %f", r2.Score)
	}
}

func TestAnalyze_Japanese(t *testing.T) {
	posTests := []string{
		"今日は嬉しいことがあった",
		"ありがとうございます",
		"最高の結果だった",
		"楽しい一日だった",
	}
	for _, text := range posTests {
		r := Analyze(text)
		if r.Score <= 0 {
			t.Errorf("JP positive %q: score=%f, want > 0 (keywords=%v)", text, r.Score, r.Keywords)
		}
	}

	negTests := []string{
		"悲しいニュースだった",
		"最悪の体験だった",
		"疲れた、もうダメ",
		"嫌いな食べ物",
	}
	for _, text := range negTests {
		r := Analyze(text)
		if r.Score >= 0 {
			t.Errorf("JP negative %q: score=%f, want < 0 (keywords=%v)", text, r.Score, r.Keywords)
		}
	}
}

func TestAnalyze_Chinese(t *testing.T) {
	posTests := []string{
		"我今天很开心",
		"谢谢你的帮助",
		"太好了",
		"这个很棒",
	}
	for _, text := range posTests {
		r := Analyze(text)
		if r.Score <= 0 {
			t.Errorf("CN positive %q: score=%f, want > 0 (keywords=%v)", text, r.Score, r.Keywords)
		}
	}

	negTests := []string{
		"我很难过",
		"太糟糕了",
		"我讨厌这个",
		"真烦",
	}
	for _, text := range negTests {
		r := Analyze(text)
		if r.Score >= 0 {
			t.Errorf("CN negative %q: score=%f, want < 0 (keywords=%v)", text, r.Score, r.Keywords)
		}
	}
}

func TestAnalyze_Emoji(t *testing.T) {
	posEmoji := "Great work! \U0001F60A\U0001F389"
	r := Analyze(posEmoji)
	if r.Score <= 0 {
		t.Errorf("positive emoji %q: score=%f, want > 0", posEmoji, r.Score)
	}

	negEmoji := "\U0001F622\U0001F621 terrible"
	r2 := Analyze(negEmoji)
	if r2.Score >= 0 {
		t.Errorf("negative emoji %q: score=%f, want < 0", negEmoji, r2.Score)
	}
}

func TestAnalyze_Neutral(t *testing.T) {
	neutralTexts := []string{
		"The meeting is at 3pm",
		"Please send me the report",
		"Hello world",
		"1234567890",
		"",
	}
	for _, text := range neutralTexts {
		r := Analyze(text)
		if r.Score != 0 {
			t.Errorf("neutral %q: score=%f, want 0 (keywords=%v)", text, r.Score, r.Keywords)
		}
	}
}

func TestAnalyze_CaseInsensitive(t *testing.T) {
	r1 := Analyze("HAPPY")
	r2 := Analyze("happy")
	if r1.Score != r2.Score {
		t.Errorf("case sensitivity: HAPPY=%f, happy=%f", r1.Score, r2.Score)
	}
}

func TestAnalyze_ScoreClamped(t *testing.T) {
	// Many positive keywords should still clamp to 1.0.
	text := "happy great love awesome thanks perfect excellent wonderful amazing"
	r := Analyze(text)
	if r.Score > 1.0 || r.Score < -1.0 {
		t.Errorf("score out of range: %f", r.Score)
	}
	if r.Score != 1.0 {
		t.Errorf("all-positive should be 1.0, got %f", r.Score)
	}
}

func TestContainsWord(t *testing.T) {
	tests := []struct {
		text, word string
		want       bool
	}{
		{"i am happy", "happy", true},
		{"unhappy day", "happy", false},
		{"happy!", "happy", true},
		{"i'm sad today", "sad", true},
		{"bad luck", "bad", true},
		{"badly done", "bad", false},
	}
	for _, tt := range tests {
		got := ContainsWord(tt.text, tt.word)
		if got != tt.want {
			t.Errorf("ContainsWord(%q, %q) = %v, want %v", tt.text, tt.word, got, tt.want)
		}
	}
}

func TestLabel(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{0.8, "positive"},
		{-0.7, "negative"},
		{0.3, "slightly positive"},
		{-0.3, "slightly negative"},
		{0.0, "neutral"},
		{0.05, "neutral"},
	}
	for _, tt := range tests {
		got := Label(tt.score)
		if got != tt.want {
			t.Errorf("Label(%f) = %q, want %q", tt.score, got, tt.want)
		}
	}
}
