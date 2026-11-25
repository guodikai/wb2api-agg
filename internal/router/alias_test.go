package router

import (
	"reflect"
	"testing"
)

// 用例都取自三家真实模型名（2026-11-26）：
//   WorkBuddy 带 cn: 前缀且大小写不一；码道/豆包裸名。
func TestNormalize(t *testing.T) {
	cases := []struct {
		in        string
		key       string
		matchable bool
	}{
		{"cn:DeepSeek-V4-flash", "deepseek-v4-flash", true},
		{"deepseek-v4-flash", "deepseek-v4-flash", true},
		{"CN:DeepSeek-V4-Flash", "deepseek-v4-flash", true},
		{"global:gpt-6-luna", "gpt-6-luna", true},
		{"codearts:deepseek-v3", "deepseek-v3", true},
		{"doubao:dogfood-seed-1.8", "dogfood-seed-1.8", true},
		{"cn:codearts:deepseek-v3", "deepseek-v3", true}, // 前缀可叠加

		// 档位别名：平台自己再调度，跨平台不可比
		{"cn:fast-model", "fast-model", false},
		{"cn:balanced-model", "balanced-model", false},
		{"cn:deep-model", "deep-model", false},
		{"cn:auto", "auto", false},

		// 版本点必须保留，v4 与 v4.1 是两个模型
		{"cn:deepseek-v4.1-flash", "deepseek-v4.1-flash", true},

		// 分隔符统一
		{"DeepSeek_V4 Flash", "deepseek-v4-flash", true},

		// 非平台前缀不得误剥（模型自带命名空间）
		{"qwen:max", "qwen:max", true},

		// 空值
		{"", "", false},
		{"   ", "", false},
	}
	for _, c := range cases {
		key, ok := Normalize(c.in)
		if key != c.key || ok != c.matchable {
			t.Errorf("Normalize(%q) = (%q,%v), want (%q,%v)", c.in, key, ok, c.key, c.matchable)
		}
	}
}

func TestGroupSameNameAcrossChannels(t *testing.T) {
	entries := []Entry{
		{Channel: "workbuddy", Model: "cn:DeepSeek-V4-flash", UnitCost: 0.5, CostKnown: true},
		{Channel: "codearts", Model: "deepseek-v4-flash", UnitCost: 0.3, CostKnown: true},
		{Channel: "doubao", Model: "doubao:deepseek-v4-flash", CostKnown: false},
		{Channel: "workbuddy", Model: "cn:fast-model"},      // 档位别名 → local
		{Channel: "workbuddy", Model: "cn:hunyuan-image-alpha", CostKnown: false}, // 单平台独有
	}
	groups, local := Group(entries)

	if got := len(groups["deepseek-v4-flash"]); got != 3 {
		t.Fatalf("deepseek-v4-flash 应聚到 3 个候选，实际 %d", got)
	}
	if _, ok := groups["fast-model"]; ok {
		t.Error("档位别名不应进入分组")
	}
	if len(local) != 1 || local[0].Model != "cn:fast-model" {
		t.Errorf("local 应为 [cn:fast-model]，实际 %+v", local)
	}
	// 单平台独有模型仍会成组（组大小 1），调用方自行决定是否暴露
	if got := len(groups["hunyuan-image-alpha"]); got != 1 {
		t.Errorf("hunyuan-image-alpha 应单独成组，实际 %d", got)
	}
}

func TestOrderByUnitCostThenChannel(t *testing.T) {
	in := []Entry{
		{Channel: "doubao", Model: "x", CostKnown: false},
		{Channel: "workbuddy", Model: "x", UnitCost: 0.5, CostKnown: true},
		{Channel: "codearts", Model: "x", UnitCost: 0.3, CostKnown: true},
		{Channel: "aaa", Model: "x", CostKnown: false},
	}
	got := Order(in)
	want := []string{"codearts", "workbuddy", "aaa", "doubao"}
	for i, e := range got {
		if e.Channel != want[i] {
			t.Fatalf("顺序 = %v, want %v", channelsOf(got), want)
		}
	}
}

func TestOrderDoesNotMutateInput(t *testing.T) {
	in := []Entry{{Channel: "b", CostKnown: true, UnitCost: 2}, {Channel: "a", CostKnown: true, UnitCost: 1}}
	orig := append([]Entry(nil), in...)
	_ = Order(in)
	if !reflect.DeepEqual(in, orig) {
		t.Error("Order 不应修改入参")
	}
}

func channelsOf(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Channel
	}
	return out
}
