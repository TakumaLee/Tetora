package main

import (
	"context"
	"net/url"
	"strings"
	"testing"
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
