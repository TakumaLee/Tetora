package main

import (
	"context"
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
