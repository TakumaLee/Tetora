package main

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// expandPrompt
// ---------------------------------------------------------------------------

func TestExpandPrompt_NoTemplateVariables(t *testing.T) {
	got := expandPrompt("hello world", "", "", "", "", nil)
	if got != "hello world" {
		t.Errorf("expandPrompt(%q) = %q, want %q", "hello world", got, "hello world")
	}
}

func TestExpandPrompt_DateReplacement(t *testing.T) {
	got := expandPrompt("Today is {{date}}", "", "", "", "", nil)
	want := "Today is " + time.Now().Format("2006-01-02")
	if got != want {
		t.Errorf("expandPrompt with {{date}} = %q, want %q", got, want)
	}
}

func TestExpandPrompt_WeekdayReplacement(t *testing.T) {
	got := expandPrompt("Day: {{weekday}}", "", "", "", "", nil)
	want := "Day: " + time.Now().Weekday().String()
	if got != want {
		t.Errorf("expandPrompt with {{weekday}} = %q, want %q", got, want)
	}
}

func TestExpandPrompt_EnvVarSet(t *testing.T) {
	t.Setenv("TETORA_TEST_TMPL", "foo")

	got := expandPrompt("Hello {{env.TETORA_TEST_TMPL}}", "", "", "", "", nil)
	if got != "Hello foo" {
		t.Errorf("expandPrompt with {{env.TETORA_TEST_TMPL}} = %q, want %q", got, "Hello foo")
	}
}

func TestExpandPrompt_EnvVarUnset(t *testing.T) {
	got := expandPrompt("Val={{env.TETORA_UNSET_VAR_99999}}", "", "", "", "", nil)
	if got != "Val=" {
		t.Errorf("expandPrompt with unset env var = %q, want %q", got, "Val=")
	}
}

func TestExpandPrompt_MultipleVariables(t *testing.T) {
	got := expandPrompt("Date: {{date}}, Day: {{weekday}}", "", "", "", "", nil)
	wantDate := time.Now().Format("2006-01-02")
	wantWeekday := time.Now().Weekday().String()
	want := "Date: " + wantDate + ", Day: " + wantWeekday
	if got != want {
		t.Errorf("expandPrompt with multiple vars = %q, want %q", got, want)
	}
}

func TestExpandPrompt_LastOutputWithEmptyJobIDAndDBPath(t *testing.T) {
	input := "Previous: {{last_output}}"
	got := expandPrompt(input, "", "", "", "", nil)
	// When jobID and dbPath are both empty, last_* variables are not replaced.
	if got != input {
		t.Errorf("expandPrompt with empty jobID/dbPath = %q, want %q (unchanged)", got, input)
	}
}
