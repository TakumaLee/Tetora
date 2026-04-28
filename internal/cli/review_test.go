package cli

import "testing"

func TestNormalizeReviewTarget(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"https_passthrough", "https://github.com/a/b/pull/1", "https://github.com/a/b/pull/1", false},
		{"http_passthrough", "http://gitlab.com/a/b/-/merge_requests/2", "http://gitlab.com/a/b/-/merge_requests/2", false},
		{"shorthand_github", "TakumaLee/tetora#99", "https://github.com/TakumaLee/tetora/pull/99", false},
		{"empty", "", "", true},
		{"bare_word", "notaurl", "", true},
		{"shorthand_missing_num", "a/b#", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeReviewTarget(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
