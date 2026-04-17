package config

import "testing"

func TestIsKougyokuProject(t *testing.T) {
	projects := []string{"tetora", "goldenfish", "sentori"}

	tests := []struct {
		name    string
		workdir string
		want    bool
	}{
		{"tetora パスが一致", "/Users/user/projects/tetora", true},
		{"goldenfish パスが一致", "/home/dev/goldenfish/src", true},
		{"sentori パスが一致", "/workspace/sentori", true},
		{"lookr パスは不一致", "/Users/user/projects/lookr", false},
		{"moviebonus パスは不一致", "/projects/moviebonus", false},
		{"空 workdir は false", "", false},
		{"大文字小文字を区別しない一致", "/projects/Tetora", true},
		{"パス区切り内の部分一致", "/projects/tetora-sub", true},
		{"プロジェクト名のみ渡された場合も一致", "tetora", true},
		{"プロジェクト名のみ — 不一致", "lookr", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsKougyokuProject(tt.workdir, projects); got != tt.want {
				t.Errorf("IsKougyokuProject(%q, %v) = %v, want %v", tt.workdir, projects, got, tt.want)
			}
		})
	}
}

func TestKougyokuProjectsOrDefault(t *testing.T) {
	t.Run("設定なし → デフォルト3プロジェクト", func(t *testing.T) {
		cfg := SmartDispatchConfig{}
		got := cfg.KougyokuProjectsOrDefault()
		want := []string{"tetora", "goldenfish", "sentori"}
		if len(got) != len(want) {
			t.Fatalf("got len=%d %v, want %v", len(got), got, want)
		}
		for i, v := range want {
			if got[i] != v {
				t.Errorf("got[%d] = %q, want %q", i, got[i], v)
			}
		}
	})

	t.Run("カスタム設定 → カスタム値を返す", func(t *testing.T) {
		cfg := SmartDispatchConfig{KougyokuProjects: []string{"myproject", "other"}}
		got := cfg.KougyokuProjectsOrDefault()
		if len(got) != 2 || got[0] != "myproject" || got[1] != "other" {
			t.Errorf("got %v, want [myproject other]", got)
		}
	})

	t.Run("nil スライス → デフォルト3プロジェクト", func(t *testing.T) {
		cfg := SmartDispatchConfig{KougyokuProjects: nil}
		got := cfg.KougyokuProjectsOrDefault()
		if len(got) != 3 {
			t.Errorf("expected 3 defaults, got %d: %v", len(got), got)
		}
	})

	t.Run("空スライス → デフォルト3プロジェクト", func(t *testing.T) {
		cfg := SmartDispatchConfig{KougyokuProjects: []string{}}
		got := cfg.KougyokuProjectsOrDefault()
		if len(got) != 3 {
			t.Errorf("expected 3 defaults, got %d: %v", len(got), got)
		}
	})
}
