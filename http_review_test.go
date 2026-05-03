package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTruncateDiffLines(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		max        int
		wantLines  int
		wantDrop   int
	}{
		{"under_cap", "a\nb\nc", 10, 3, 0},
		{"at_cap", "a\nb\nc", 3, 3, 0},
		{"over_cap", "a\nb\nc\nd\ne", 3, 3, 2},
		{"zero_max_passthrough", "a\nb\nc", 0, 3, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, drop := truncateDiffLines(tc.in, tc.max)
			gotLines := 1 + strings.Count(out, "\n")
			if out == "" {
				gotLines = 0
			}
			if gotLines != tc.wantLines {
				t.Fatalf("lines = %d, want %d (out=%q)", gotLines, tc.wantLines, out)
			}
			if drop != tc.wantDrop {
				t.Fatalf("dropped = %d, want %d", drop, tc.wantDrop)
			}
		})
	}
}

func TestFetchReviewDiff_UnsupportedHost(t *testing.T) {
	_, _, err := fetchReviewDiff("https://bitbucket.org/foo/bar/pull-requests/1")
	if err == nil {
		t.Fatal("expected error for unsupported host")
	}
	if !strings.Contains(err.Error(), "untrusted review host") {
		t.Fatalf("expected 'untrusted review host' error, got %v", err)
	}
}

func TestFetchReviewDiff_InvalidURL(t *testing.T) {
	_, _, err := fetchReviewDiff("://not-a-url")
	if err == nil {
		t.Fatal("expected error for invalid url")
	}
}

func TestFetchReviewDiff_SSRFBlocked(t *testing.T) {
	_, _, err := fetchReviewDiff("https://gitlab.evil.com/org/repo/-/merge_requests/1")
	if err == nil || !strings.Contains(err.Error(), "untrusted review host") {
		t.Fatalf("expected SSRF block, got %v", err)
	}
}

func TestPostReviewComment_UnsupportedHost(t *testing.T) {
	err := postReviewComment(context.Background(), "https://bitbucket.org/foo/bar/pull-requests/1", "body")
	if err == nil || !strings.Contains(err.Error(), "untrusted review host") {
		t.Fatalf("expected untrusted host error, got %v", err)
	}
}

func TestPostReviewComment_SSRFBlocked(t *testing.T) {
	err := postReviewComment(context.Background(), "https://gitlab.evil.com/org/repo/-/merge_requests/1", "body")
	if err == nil || !strings.Contains(err.Error(), "untrusted review host") {
		t.Fatalf("expected SSRF block, got %v", err)
	}
}

func TestPostReviewComment_InvalidURL(t *testing.T) {
	err := postReviewComment(context.Background(), "://not-a-url", "body")
	if err == nil {
		t.Fatal("expected error for invalid url")
	}
}

func TestPostReviewComment_GitLabBadURL(t *testing.T) {
	err := postReviewComment(context.Background(), "https://gitlab.com/no-mr-id-here", "body")
	if err == nil || !strings.Contains(err.Error(), "unrecognized GitLab MR URL") {
		t.Fatalf("expected unrecognized GitLab MR URL error, got %v", err)
	}
}

// TestReviewCommentCmdArgs locks down the exact CLI args used to post comments.
// This code has regressed multiple times — flapping between glab flags
// (-F / -f / --form). Per `glab api --help`:
//   -F/--field reads `@file` as JSON string field (correct for note body)
//   --form does multipart upload (wrong — uploads body as binary attachment)
// If you change this, update the test and the comment in reviewCommentCmdArgs.
// makeComments builds n prContextItems with authors "comment-1" .. "comment-N"
// for use in formatter tests.
func makeComments(n int) []prContextItem {
	items := make([]prContextItem, n)
	for i := range items {
		items[i] = prContextItem{
			Author:    fmt.Sprintf("comment-%d", i+1),
			Body:      fmt.Sprintf("body of comment %d", i+1),
			CreatedAt: time.Unix(int64(i*3600), 0),
		}
	}
	return items
}

func TestFormatPRContext(t *testing.T) {
	t.Run("last_two_only", func(t *testing.T) {
		view := prContextView{Comments: makeComments(10)}
		out := formatPRContext(view)
		// Authors are embedded in "Comment by comment-N (created …):" lines.
		// Check for the exact "Comment by comment-N " pattern to avoid
		// "comment-1" matching as a prefix of "comment-10".
		for _, want := range []string{"Comment by comment-9 ", "Comment by comment-10 "} {
			if !strings.Contains(out, want) {
				t.Errorf("expected %q in output, got:\n%s", want, out)
			}
		}
		for i := 1; i <= 8; i++ {
			name := fmt.Sprintf("Comment by comment-%d ", i)
			if strings.Contains(out, name) {
				t.Errorf("unexpected %q in output (should be dropped), got:\n%s", name, out)
			}
		}
	})

	t.Run("chronological_order", func(t *testing.T) {
		view := prContextView{Comments: makeComments(10)}
		out := formatPRContext(view)
		// Use the "Comment by comment-N " pattern to avoid substring false-match.
		pos9 := strings.Index(out, "Comment by comment-9 ")
		pos10 := strings.Index(out, "Comment by comment-10 ")
		if pos9 < 0 || pos10 < 0 {
			t.Fatalf("missing comment-9 or comment-10 in output: %s", out)
		}
		if pos9 >= pos10 {
			t.Errorf("expected comment-9 before comment-10 (oldest-first), but positions are %d >= %d", pos9, pos10)
		}
	})

	t.Run("system_notes_filtered", func(t *testing.T) {
		// The system filter is at the fetch layer, not in formatPRContext.
		// All items passed to the formatter should be emitted (no dedup/skip here).
		items := []prContextItem{
			{Author: "alice", Body: "body-alice", CreatedAt: time.Unix(1000, 0)},
			{Author: "bob", Body: "body-bob", CreatedAt: time.Unix(2000, 0)},
			{Author: "carol", Body: "body-carol", CreatedAt: time.Unix(3000, 0)},
		}
		view := prContextView{Comments: items}
		out := formatPRContext(view)
		// Only last 2 shown (bob and carol); alice is dropped by lastN.
		if strings.Contains(out, "alice") {
			t.Errorf("alice should be dropped by lastN(2), got: %s", out)
		}
		if !strings.Contains(out, "bob") {
			t.Errorf("bob should appear in output, got: %s", out)
		}
		if !strings.Contains(out, "carol") {
			t.Errorf("carol should appear in output, got: %s", out)
		}
	})

	t.Run("description_does_not_displace_latest", func(t *testing.T) {
		desc := strings.Repeat("x", 50_000)
		view := prContextView{
			Title:       "big desc PR",
			Description: desc,
			Comments:    makeComments(5),
		}
		out := formatPRContext(view)
		// Latest two comments must always appear regardless of description size.
		if !strings.Contains(out, "comment-4") {
			t.Errorf("comment-4 missing from output with large description")
		}
		if !strings.Contains(out, "comment-5") {
			t.Errorf("comment-5 missing from output with large description")
		}
	})

	t.Run("middle_elision", func(t *testing.T) {
		// Build a 50KB comment body with distinctive head and tail.
		head100 := strings.Repeat("H", 100)
		tail100 := strings.Repeat("T", 100)
		padding := strings.Repeat("M", 50_000-200)
		body := head100 + padding + tail100

		view := prContextView{
			Comments: []prContextItem{
				{Author: "author-elide", Body: body, CreatedAt: time.Unix(0, 0)},
			},
		}
		out := formatPRContext(view)
		if !strings.Contains(out, "[truncated middle:") {
			t.Errorf("expected truncation marker in output, got: %s", out[:min(200, len(out))])
		}
		if !strings.Contains(out, head100) {
			t.Errorf("expected head preserved in output")
		}
		if !strings.Contains(out, tail100) {
			t.Errorf("expected tail preserved in output")
		}
	})

	t.Run("rune_boundary", func(t *testing.T) {
		// "中" is 3 bytes in UTF-8; 20000 runes = 60000 bytes > 32KB threshold.
		body := strings.Repeat("中", 20_000)
		view := prContextView{
			Comments: []prContextItem{
				{Author: "author-rune", Body: body, CreatedAt: time.Unix(0, 0)},
			},
		}
		out := formatPRContext(view)
		// No UTF-8 replacement character (U+FFFD) in output.
		if strings.ContainsRune(out, utf8.RuneError) {
			t.Errorf("output contains UTF-8 replacement char (rune boundary not respected)")
		}
		if !strings.Contains(out, "[truncated middle:") {
			t.Errorf("expected truncation marker for 60KB input")
		}
	})

	t.Run("empty", func(t *testing.T) {
		out := formatPRContext(prContextView{})
		if out != "" {
			t.Errorf("expected empty string for empty view, got: %q", out)
		}
	})
}

func TestElideMiddle(t *testing.T) {
	t.Run("small_passthrough", func(t *testing.T) {
		body := "hello world"
		got := elideMiddle(body, 32_000)
		if got != body {
			t.Errorf("small input should be unchanged: got %q", got)
		}
	})

	t.Run("exact_boundary_passthrough", func(t *testing.T) {
		body := strings.Repeat("a", 32_000)
		got := elideMiddle(body, 32_000)
		if got != body {
			t.Errorf("input at exact limit should be unchanged")
		}
	})

	t.Run("large_has_marker", func(t *testing.T) {
		body := strings.Repeat("a", 50_000)
		got := elideMiddle(body, 32_000)
		if !strings.Contains(got, "[truncated middle:") {
			t.Errorf("expected truncation marker; got length %d", len(got))
		}
	})

	t.Run("rune_safe", func(t *testing.T) {
		// 20 000 × "中" (3 bytes each) = 60 000 bytes.
		body := strings.Repeat("中", 20_000)
		got := elideMiddle(body, 32_000)
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Errorf("elideMiddle produced invalid UTF-8 (rune sliced)")
		}
	})
}

func TestReviewCommentCmdArgs(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantBin string
		wantHas []string
		wantNot []string
	}{
		{
			name:    "github_pr",
			raw:     "https://github.com/owner/repo/pull/42",
			wantBin: "gh",
			wantHas: []string{"pr", "comment", "https://github.com/owner/repo/pull/42", "--body-file", "/tmp/body.md"},
		},
		{
			name:    "gitlab_mr",
			raw:     "https://gitlab.com/group/sub/repo/-/merge_requests/40",
			wantBin: "glab",
			wantHas: []string{"api", "--hostname", "gitlab.com", "-X", "POST", "projects/group%2Fsub%2Frepo/merge_requests/40/notes", "-F", "body=@/tmp/body.md"},
			wantNot: []string{"--form", "-f"}, // -f is --raw-field in glab (no @file support); --form does multipart
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			bin, args, err := reviewCommentCmdArgs(u, tc.raw, "/tmp/body.md")
			if err != nil {
				t.Fatalf("reviewCommentCmdArgs: %v", err)
			}
			if bin != tc.wantBin {
				t.Fatalf("bin = %q, want %q", bin, tc.wantBin)
			}
			joined := strings.Join(args, " ")
			for _, want := range tc.wantHas {
				found := false
				for _, a := range args {
					if a == want {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("args missing %q; got: %s", want, joined)
				}
			}
			for _, bad := range tc.wantNot {
				for _, a := range args {
					if a == bad {
						t.Fatalf("args must NOT contain %q (regression risk); got: %s", bad, joined)
					}
				}
			}
		})
	}
}

func TestBuildCrossRefSummary(t *testing.T) {
	t.Run("detects_cross_file_call", func(t *testing.T) {
		// resolveAgentDir defined in watcher.go, called from configure.go —
		// the exact pattern that caused a false "duplication" review finding.
		diff := "diff --git a/internal/agent/watcher.go b/internal/agent/watcher.go\n" +
			"new file mode 100644\n--- /dev/null\n+++ b/internal/agent/watcher.go\n" +
			"@@ -0,0 +1,4 @@\n+package agent\n" +
			"+func resolveAgentDir(agentsDir, assignee string) (string, error) {\n" +
			"+\treturn agentsDir + \"/\" + assignee, nil\n+}\n" +
			"diff --git a/internal/agent/configure.go b/internal/agent/configure.go\n" +
			"new file mode 100644\n--- /dev/null\n+++ b/internal/agent/configure.go\n" +
			"@@ -0,0 +1,5 @@\n+package agent\n" +
			"+func Configure(agentsDir, name string) error {\n" +
			"+\tdir, err := resolveAgentDir(agentsDir, name)\n" +
			"+\t_ = dir\n+\treturn err\n+}\n"
		out := buildCrossRefSummary(diff)
		if !strings.Contains(out, "resolveAgentDir") {
			t.Errorf("expected resolveAgentDir in xref summary, got:\n%s", out)
		}
		if !strings.Contains(out, "watcher.go") {
			t.Errorf("expected watcher.go (definition file) in xref summary, got:\n%s", out)
		}
		if !strings.Contains(out, "configure.go") {
			t.Errorf("expected configure.go (call site) in xref summary, got:\n%s", out)
		}
	})

	t.Run("no_cross_file_refs_returns_empty", func(t *testing.T) {
		diff := "diff --git a/internal/foo/foo.go b/internal/foo/foo.go\n" +
			"--- /dev/null\n+++ b/internal/foo/foo.go\n@@ -0,0 +1,5 @@\n" +
			"+package foo\n+func helper() string { return \"x\" }\n" +
			"+func DoSomething() string {\n+\treturn helper()\n+}\n"
		if out := buildCrossRefSummary(diff); out != "" {
			t.Errorf("expected empty summary for same-file calls, got:\n%s", out)
		}
	})

	t.Run("non_go_files_ignored", func(t *testing.T) {
		diff := "diff --git a/script.sh b/script.sh\n--- /dev/null\n+++ b/script.sh\n" +
			"@@ -0,0 +1,1 @@\n+function myFunc() { echo hi; }\n" +
			"diff --git a/other.sh b/other.sh\n--- /dev/null\n+++ b/other.sh\n" +
			"@@ -0,0 +1,1 @@\n+myFunc\n"
		if out := buildCrossRefSummary(diff); out != "" {
			t.Errorf("expected empty summary for non-Go files, got:\n%s", out)
		}
	})

	t.Run("empty_diff_returns_empty", func(t *testing.T) {
		if got := buildCrossRefSummary(""); got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("multiple_callers", func(t *testing.T) {
		diff := "diff --git a/pkg/a.go b/pkg/a.go\n--- /dev/null\n+++ b/pkg/a.go\n" +
			"@@ -0,0 +1,2 @@\n+package pkg\n+func Shared() string { return \"x\" }\n" +
			"diff --git a/pkg/b.go b/pkg/b.go\n--- /dev/null\n+++ b/pkg/b.go\n" +
			"@@ -0,0 +1,2 @@\n+package pkg\n+func UseB() string { return Shared() }\n" +
			"diff --git a/pkg/c.go b/pkg/c.go\n--- /dev/null\n+++ b/pkg/c.go\n" +
			"@@ -0,0 +1,2 @@\n+package pkg\n+func UseC() string { return Shared() }\n"
		out := buildCrossRefSummary(diff)
		if !strings.Contains(out, "pkg/b.go") || !strings.Contains(out, "pkg/c.go") {
			t.Errorf("expected both callers listed, got:\n%s", out)
		}
	})
}
