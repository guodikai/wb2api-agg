package agg

import (
	"testing"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
)

func TestSplitModelChannelPrefixes(t *testing.T) {
	cases := []struct {
		in       string
		kind     channel.Kind
		explicit bool
		model    string
	}{
		{"codearts:deepseek-v3", channel.KindCodeArts, true, "deepseek-v3"},
		{"CodeArts:deepseek-v3", channel.KindCodeArts, true, "deepseek-v3"}, // 前缀大小写不敏感
		{"doubao:DeepSeek Flash", channel.KindDoubao, true, "DeepSeek Flash"},
		{"workbuddy:cn:deepseek-v4-flash", channel.KindWorkBuddy, true, "cn:deepseek-v4-flash"},

		// 非通道前缀：cn/global 是 WorkBuddy 的 realm，必须原样保留
		{"cn:deepseek-v4-flash", "", false, "cn:deepseek-v4-flash"},
		{"global:gpt-6-luna", "", false, "global:gpt-6-luna"},
		{"hy4flash-free", "", false, "hy4flash-free"},
		{"qwen:max", "", false, "qwen:max"}, // 模型自带命名空间，不是通道
		{"", "", false, ""},
		{"  doubao:x  ", channel.KindDoubao, true, "x"},
	}
	for _, c := range cases {
		got := SplitModel(c.in)
		if got.Kind != c.kind || got.Explicit != c.explicit || got.Model != c.model {
			t.Errorf("SplitModel(%q) = {%q,%v,%q}, want {%q,%v,%q}",
				c.in, got.Kind, got.Explicit, got.Model, c.kind, c.explicit, c.model)
		}
	}
}

func TestQualifyKeepsWorkBuddyBare(t *testing.T) {
	if got := Qualify(channel.KindWorkBuddy, "cn:deepseek-v4-flash"); got != "cn:deepseek-v4-flash" {
		t.Errorf("WorkBuddy 应保持裸 id（零回归），实际 %q", got)
	}
	if got := Qualify(channel.KindCodeArts, "deepseek-v3"); got != "codearts:deepseek-v3" {
		t.Errorf("码道应加前缀，实际 %q", got)
	}
	if got := Qualify("", "x"); got != "x" {
		t.Errorf("空通道应原样返回，实际 %q", got)
	}
}

func TestSplitQualifyRoundTrip(t *testing.T) {
	for _, m := range []string{"codearts:deepseek-v3", "doubao:DeepSeek Flash"} {
		tg := SplitModel(m)
		if got := Qualify(tg.Kind, tg.Model); got != m {
			t.Errorf("往返不一致: %q → %q", m, got)
		}
	}
}
