package rule

import "testing"

func TestMatch_AlwaysAndMatched(t *testing.T) {
	entries := []Entry{
		{Keywords: []string{"發文", "medium"}, Path: "social-media.md"},
		{Keywords: []string{"回覆"}, Path: "reply-format.md", Always: true},
		{Keywords: []string{"polymarket", "翡翠"}, Path: "hisui-market-scanning.md"},
	}
	always, matched := Match(entries, "幫我發一篇 Medium 文")
	if len(always) != 1 || always[0].Path != "reply-format.md" {
		t.Errorf("always = %+v", always)
	}
	if len(matched) != 1 || matched[0].Path != "social-media.md" {
		t.Errorf("matched = %+v", matched)
	}
}

func TestMatch_CaseInsensitive(t *testing.T) {
	entries := []Entry{
		{Keywords: []string{"medium"}, Path: "social-media.md"},
	}
	_, matched := Match(entries, "post a MEDIUM article")
	if len(matched) != 1 {
		t.Errorf("expected case-insensitive hit, got %+v", matched)
	}
}

func TestMatch_NoMatch(t *testing.T) {
	entries := []Entry{
		{Keywords: []string{"polymarket"}, Path: "hisui.md"},
	}
	always, matched := Match(entries, "dispatch 黑曜 調查 bug")
	if len(always) != 0 || len(matched) != 0 {
		t.Errorf("expected no matches; always=%+v matched=%+v", always, matched)
	}
}

func TestMatch_AlwaysSkipsKeywordLoop(t *testing.T) {
	// An always-on entry should not also appear in matched even if keywords match.
	entries := []Entry{
		{Keywords: []string{"git"}, Path: "git-hygiene.md", Always: true},
	}
	always, matched := Match(entries, "do git commit")
	if len(always) != 1 || len(matched) != 0 {
		t.Errorf("always-on must not double-match; always=%+v matched=%+v", always, matched)
	}
}
