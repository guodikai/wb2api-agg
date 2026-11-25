package agg

import (
	"errors"
	"strings"
	"testing"
)

func TestChooseCheapestKnownCost(t *testing.T) {
	d, err := Choose("deepseek-v4-flash", []Candidate{
		{Channel: "workbuddy", Model: "cn:deepseek-v4.1-flash", UnitCost: 0.5, CostKnown: true},
		{Channel: "codearts", Model: "deepseek-v4-flash", UnitCost: 0.3, CostKnown: true},
		{Channel: "doubao", Model: "DeepSeek Flash", CostKnown: false},
	})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if d.Channel != "codearts" || d.Model != "deepseek-v4-flash" {
		t.Fatalf("应选 codearts，实际 %+v", d)
	}
	if !strings.Contains(d.Reason, "cost 0.30") {
		t.Errorf("reason 应带成本: %q", d.Reason)
	}
}

func TestChooseUnknownCostGoesLast(t *testing.T) {
	d, _ := Choose("m", []Candidate{
		{Channel: "a", Model: "m", CostKnown: false},
		{Channel: "b", Model: "m", UnitCost: 9, CostKnown: true},
	})
	if d.Channel != "b" {
		t.Fatalf("已知成本应优先，实际 %+v", d)
	}
}

func TestChooseSkipsCooling(t *testing.T) {
	d, err := Choose("m", []Candidate{
		{Channel: "workbuddy", Model: "m", UnitCost: 0.1, CostKnown: true, Cooling: true},
		{Channel: "codearts", Model: "m", UnitCost: 0.9, CostKnown: true},
	})
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if d.Channel != "codearts" {
		t.Fatalf("冷却平台应被跳过，实际 %+v", d)
	}
	if !strings.Contains(d.Reason, "1 cooling skipped") {
		t.Errorf("reason 应记录跳过数: %q", d.Reason)
	}
}

func TestChooseAllCoolingIsError(t *testing.T) {
	_, err := Choose("m", []Candidate{
		{Channel: "a", Model: "m", Cooling: true},
		{Channel: "b", Model: "m", Cooling: true},
	})
	if !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("全冷却应返回 ErrNoCandidate，实际 %v", err)
	}
}

func TestChooseEmptyIsError(t *testing.T) {
	_, err := Choose("m", nil)
	if !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("空候选应返回 ErrNoCandidate，实际 %v", err)
	}
}

func TestChooseTieIsStable(t *testing.T) {
	// 同成本时必须稳定（按平台名），否则同一请求会在两平台间抖动。
	for i := 0; i < 20; i++ {
		d, _ := Choose("m", []Candidate{
			{Channel: "workbuddy", Model: "m", UnitCost: 1, CostKnown: true},
			{Channel: "codearts", Model: "m", UnitCost: 1, CostKnown: true},
		})
		if d.Channel != "codearts" {
			t.Fatalf("稳定排序应选 codearts（字典序在前），实际 %s", d.Channel)
		}
	}
}

func TestChooseDoesNotMutateInput(t *testing.T) {
	in := []Candidate{
		{Channel: "b", Model: "m", UnitCost: 2, CostKnown: true},
		{Channel: "a", Model: "m", UnitCost: 1, CostKnown: true},
	}
	if _, err := Choose("m", in); err != nil {
		t.Fatal(err)
	}
	if in[0].Channel != "b" || in[1].Channel != "a" {
		t.Errorf("Choose 不应修改入参顺序: %+v", in)
	}
}
