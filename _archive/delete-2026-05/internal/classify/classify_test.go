package classify_test

import (
	"strings"
	"testing"

	"tetora/internal/classify"
)

// --- String method ---

func TestComplexityString(t *testing.T) {
	tests := []struct {
		c    classify.Complexity
		want string
	}{
		{classify.Simple, "simple"},
		{classify.Standard, "standard"},
		{classify.Complex, "complex"},
		{classify.Complexity(99), "standard"}, // unknown falls back
	}
	for _, tt := range tests {
		if got := tt.c.String(); got != tt.want {
			t.Errorf("Complexity(%d).String() = %q, want %q", int(tt.c), got, tt.want)
		}
	}
}

// --- Simple greeting detection ---

func TestClassifySimpleGreeting(t *testing.T) {
	cases := []struct {
		prompt string
		source string
	}{
		{"hello", "discord"},
		{"hi there!", "telegram"},
		{"おはよう", "line"},
		{"hey", "slack"},
		{"good morning", "whatsapp"},
		{"yo", "matrix"},
		{"sup", "teams"},
		{"hi", "signal"},
		{"hey there", "gchat"},
		{"hello!", "imessage"},
		{"How are you?", "chat"},
	}
	for _, tc := range cases {
		got := classify.Classify(tc.prompt, tc.source)
		if got != classify.Simple {
			t.Errorf("Classify(%q, %q) = %v, want Simple", tc.prompt, tc.source, got)
		}
	}
}

// --- Complex code task detection ---

func TestClassifyComplexCodingKeywords(t *testing.T) {
	cases := []struct {
		prompt string
	}{
		{"Please implement a new feature"},
		{"Debug the login endpoint"},
		{"Refactor the database schema"},
		{"Build an API for user management"},
		{"Write a function to sort the list"},
		{"Deploy the application to production"},
		{"Optimize the algorithm for speed"},
		{"Fix the SQL query performance"},
		{"Set up authentication and authorization"},
		{"Create a migration for the new table"},
		{"CODE review this pull request"},          // case-insensitive
		{"The DATABASE needs a new index"},         // case-insensitive
		{"please compile this project"},            // lowercase keyword
		{"Run the benchmark for concurrency test"}, // multiple keywords
	}
	for _, tc := range cases {
		got := classify.Classify(tc.prompt, "discord")
		if got != classify.Complex {
			t.Errorf("Classify(%q, discord) = %v, want Complex", tc.prompt, got)
		}
	}
}

func TestClassifyComplexJapaneseKeywords(t *testing.T) {
	cases := []struct {
		prompt string
	}{
		{"この関数を実装してください"},
		{"デバッグをお願いします"},
		{"リファクタリングが必要です"},  // contains リファクタ
		{"データベースのスキーマを更新して"},
		{"アルゴリズムを最適化して"},
		{"認証の仕組みを設計して"},
		{"コードレビューして"},
		{"パイプラインを構築して"},
	}
	for _, tc := range cases {
		got := classify.Classify(tc.prompt, "discord")
		if got != classify.Complex {
			t.Errorf("Classify(%q, discord) = %v, want Complex", tc.prompt, got)
		}
	}
}

// --- Standard middle-ground ---

func TestClassifyStandard(t *testing.T) {
	cases := []struct {
		prompt string
		source string
	}{
		// > 100 runes from a chat source, no coding keywords → standard
		{"Tell me about the weather in Tokyo tomorrow and what I should wear for the upcoming outdoor occasion this weekend", "discord"},
		{"Can you summarize the latest news about climate change?", "http"},
		{"What is the capital of France?", "http"},
		// Long-ish prompt from chat source but no keywords, > 100 runes
		{"I was wondering if you could help me understand the general process of how things work around here in more detail please", "discord"},
	}
	for _, tc := range cases {
		got := classify.Classify(tc.prompt, tc.source)
		if got != classify.Standard {
			t.Errorf("Classify(%q, %q) = %v, want Standard", tc.prompt, tc.source, got)
		}
	}
}

// --- CJK character length handling ---

func TestClassifyCJKLength(t *testing.T) {
	// 99 CJK characters should be < 100 rune threshold → simple from chat source
	short := strings.Repeat("あ", 99)
	if got := classify.Classify(short, "discord"); got != classify.Simple {
		t.Errorf("99 CJK runes from discord = %v, want Simple", got)
	}

	// 100 CJK characters should be >= 100 → standard (no keywords, chat source)
	exact100 := strings.Repeat("あ", 100)
	if got := classify.Classify(exact100, "discord"); got != classify.Standard {
		t.Errorf("100 CJK runes from discord = %v, want Standard", got)
	}

	// 2001 CJK characters should be > 2000 → complex
	long := strings.Repeat("漢", 2001)
	if got := classify.Classify(long, "discord"); got != classify.Complex {
		t.Errorf("2001 CJK runes from discord = %v, want Complex", got)
	}
}

// --- Source-based overrides ---

func TestClassifySourceCronKeywordBased(t *testing.T) {
	// Short cron prompt without coding keywords → Simple
	got := classify.Classify("hello", "cron")
	if got != classify.Simple {
		t.Errorf("Classify(hello, cron) = %v, want Simple", got)
	}

	// Cron prompt with coding keywords (3+) and >= 100 runes → Complex
	got2 := classify.Classify("implement the database migration and deploy the schema changes to production environment for the new user authentication feature", "cron")
	if got2 != classify.Complex {
		t.Errorf("Classify(complex cron, cron) = %v, want Complex", got2)
	}

	// Cron prompt >= 100 runes without enough coding keywords → Standard
	got3 := classify.Classify("post the daily summary to X and check engagement stats for the team then send the weekly newsletter to subscribers", "cron")
	if got3 != classify.Standard {
		t.Errorf("Classify(standard cron, cron) = %v, want Standard", got3)
	}
}

func TestClassifySourceWorkflowKeywordBased(t *testing.T) {
	// Short workflow prompt → Simple
	got := classify.Classify("check status", "workflow")
	if got != classify.Simple {
		t.Errorf("Classify(check status, workflow) = %v, want Simple", got)
	}

	// Workflow with coding keywords (3+) and >= 100 runes → Complex
	got2 := classify.Classify("refactor the api endpoint and fix the database query optimization issue that is causing slow response times in production", "workflow")
	if got2 != classify.Complex {
		t.Errorf("Classify(complex workflow, workflow) = %v, want Complex", got2)
	}

	// Workflow >= 100 runes without enough coding keywords → Standard
	got3 := classify.Classify("generate the weekly report and send it to the team channel for review please and make sure all the formatting is correct", "workflow")
	if got3 != classify.Standard {
		t.Errorf("Classify(standard workflow, workflow) = %v, want Standard", got3)
	}
}

func TestClassifySourceOverrideAgentComm(t *testing.T) {
	got := classify.Classify("ping", "agent-comm")
	if got != classify.Complex {
		t.Errorf("Classify(ping, agent-comm) = %v, want Complex", got)
	}
}

func TestClassifySourceCaseInsensitive(t *testing.T) {
	got := classify.Classify("hi", "Discord")
	if got != classify.Simple {
		t.Errorf("Classify(hi, Discord) = %v, want Simple", got)
	}

	// Short cron prompt → Simple (keyword-based, not auto-Complex)
	got2 := classify.Classify("hi", "CRON")
	if got2 != classify.Simple {
		t.Errorf("Classify(hi, CRON) = %v, want Simple", got2)
	}
}

// --- Edge cases ---

func TestClassifyEmptyString(t *testing.T) {
	// Empty prompt from chat source: length 0 < 100 → simple
	got := classify.Classify("", "discord")
	if got != classify.Simple {
		t.Errorf("Classify(empty, discord) = %v, want Simple", got)
	}

	// Empty prompt from unknown source: no keywords, length 0 < 100, but source not in ChatSources
	got2 := classify.Classify("", "http")
	if got2 != classify.Standard {
		t.Errorf("Classify(empty, http) = %v, want Standard", got2)
	}

	// Empty prompt and empty source
	got3 := classify.Classify("", "")
	if got3 != classify.Standard {
		t.Errorf("Classify(empty, empty) = %v, want Standard", got3)
	}
}

func TestClassifyExactly100Chars(t *testing.T) {
	// Exactly 100 ASCII characters, no keywords, chat source → standard (threshold is < 100)
	prompt := strings.Repeat("a", 100)
	got := classify.Classify(prompt, "discord")
	if got != classify.Standard {
		t.Errorf("100 ascii chars from discord = %v, want Standard", got)
	}

	// 99 ASCII characters, no keywords, chat source → simple
	prompt99 := strings.Repeat("a", 99)
	got2 := classify.Classify(prompt99, "discord")
	if got2 != classify.Simple {
		t.Errorf("99 ascii chars from discord = %v, want Simple", got2)
	}
}

func TestClassifyExactly2000Chars(t *testing.T) {
	// Exactly 2000 characters → not > 2000, so not auto-complex
	prompt := strings.Repeat("x", 2000)
	got := classify.Classify(prompt, "discord")
	if got != classify.Standard {
		t.Errorf("2000 chars from discord = %v, want Standard", got)
	}

	// 2001 characters → complex
	prompt2001 := strings.Repeat("x", 2001)
	got2 := classify.Classify(prompt2001, "discord")
	if got2 != classify.Complex {
		t.Errorf("2001 chars from discord = %v, want Complex", got2)
	}
}

// --- MaxSessionMessages / MaxSessionChars ---

func TestMaxSessionMessages(t *testing.T) {
	tests := []struct {
		c    classify.Complexity
		want int
	}{
		{classify.Simple, 5},
		{classify.Standard, 10},
		{classify.Complex, 20},
		{classify.Complexity(99), 10}, // unknown falls back
	}
	for _, tt := range tests {
		if got := classify.MaxSessionMessages(tt.c); got != tt.want {
			t.Errorf("MaxSessionMessages(%v) = %d, want %d", tt.c, got, tt.want)
		}
	}
}

func TestMaxSessionChars(t *testing.T) {
	tests := []struct {
		c    classify.Complexity
		want int
	}{
		{classify.Simple, 4000},
		{classify.Standard, 8000},
		{classify.Complex, 16000},
		{classify.Complexity(99), 8000}, // unknown falls back
	}
	for _, tt := range tests {
		if got := classify.MaxSessionChars(tt.c); got != tt.want {
			t.Errorf("MaxSessionChars(%v) = %d, want %d", tt.c, got, tt.want)
		}
	}
}

// --- Keyword case insensitivity ---

func TestClassifyKeywordCaseInsensitive(t *testing.T) {
	cases := []string{
		"Please IMPLEMENT this",
		"DEBUG the issue",
		"The Api is broken",
		"Fix the DATABASE",
		"SQL injection vulnerability",
		"ALGORITHM complexity",
	}
	for _, prompt := range cases {
		got := classify.Classify(prompt, "discord")
		if got != classify.Complex {
			t.Errorf("Classify(%q, discord) = %v, want Complex", prompt, got)
		}
	}
}

// --- Mixed scenarios ---

func TestClassifyShortWithKeyword(t *testing.T) {
	// Short prompt but contains a keyword → complex wins over simple.
	got := classify.Classify("fix the code", "discord")
	if got != classify.Complex {
		t.Errorf("Classify(fix the code, discord) = %v, want Complex", got)
	}
}

func TestClassifyLongFromChatNoKeywords(t *testing.T) {
	// >100 runes from a chat source, no keywords → standard.
	prompt := "I would really appreciate it if you could tell me what the weather forecast looks like for the next few days because I am planning a trip"
	got := classify.Classify(prompt, "discord")
	if got != classify.Standard {
		t.Errorf("Classify(long no-keyword, discord) = %v, want Standard", got)
	}
}

// --- ChatSources / ComplexSources maps ---

func TestChatSourcesContainsExpectedSources(t *testing.T) {
	expected := []string{"chat", "discord", "telegram", "slack", "whatsapp", "line", "matrix", "teams", "signal", "gchat", "imessage"}
	for _, src := range expected {
		if !classify.ChatSources[src] {
			t.Errorf("ChatSources missing %q", src)
		}
	}
}

func TestComplexSourcesContainsExpectedSources(t *testing.T) {
	// Only agent-comm is always Complex now
	expected := []string{"agent-comm"}
	for _, src := range expected {
		if !classify.ComplexSources[src] {
			t.Errorf("ComplexSources missing %q", src)
		}
	}
}

func TestKeywordClassifiedSourcesContainsExpectedSources(t *testing.T) {
	expected := []string{"cron", "workflow"}
	for _, src := range expected {
		if !classify.KeywordClassifiedSources[src] {
			t.Errorf("KeywordClassifiedSources missing %q", src)
		}
	}
}
