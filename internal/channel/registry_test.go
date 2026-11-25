package channel

import (
	"context"
	"errors"
	"io"
	"testing"
)

type fakeAdapter struct {
	kind  Kind
	model string
}

func (f fakeAdapter) Kind() Kind { return f.kind }

func (f fakeAdapter) Models(context.Context) ([]ModelInfo, error) {
	return []ModelInfo{{ID: f.model}}, nil
}

func (f fakeAdapter) Chat(context.Context, string, string, []byte, io.Writer) error {
	return nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakeAdapter{kind: KindWorkBuddy}); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	a, err := r.Get(KindWorkBuddy)
	if err != nil {
		t.Fatalf("取用失败: %v", err)
	}
	if a.Kind() != KindWorkBuddy {
		t.Errorf("取到错误平台: %v", a.Kind())
	}
	if !r.Has(KindWorkBuddy) {
		t.Error("Has 应为 true")
	}
}

func TestRegistryRejectsDuplicate(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(fakeAdapter{kind: KindCodeArts})
	if err := r.Register(fakeAdapter{kind: KindCodeArts}); err == nil {
		t.Fatal("重复注册必须报错（启动期暴露编程错误，不能静默覆盖）")
	}
}

func TestRegistryRejectsInvalidKindAndNil(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(fakeAdapter{kind: Kind("nope")}); err == nil {
		t.Error("未知平台应报错")
	}
	if err := r.Register(nil); err == nil {
		t.Error("nil 适配器应报错")
	}
}

func TestRegistryGetUnregistered(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get(KindDoubao)
	if !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("应返回 ErrNotRegistered，实际 %v", err)
	}
}

func TestRegistryKindsSorted(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(fakeAdapter{kind: KindWorkBuddy})
	_ = r.Register(fakeAdapter{kind: KindCodeArts})
	_ = r.Register(fakeAdapter{kind: KindDoubao})
	got := r.Kinds()
	want := []Kind{KindCodeArts, KindDoubao, KindWorkBuddy}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Kinds 顺序 = %v, want %v", got, want)
		}
	}
}
