package codearts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
)

type fakeCreds struct {
	cred  SignCredential
	err   error
	calls int
}

func (f *fakeCreds) Any(context.Context) (string, SignCredential, error) {
	f.calls++
	return "acc-1", f.cred, f.err
}

func (f *fakeCreds) For(context.Context, string) (SignCredential, error) {
	f.calls++
	if f.err != nil {
		return SignCredential{}, f.err
	}
	return f.cred, nil
}

func TestAdapterKindAndRegistry(t *testing.T) {
	a := NewAdapter(nil, &fakeCreds{}, 0)
	if a.Kind() != channel.KindCodeArts {
		t.Fatalf("Kind = %v", a.Kind())
	}
	reg := channel.NewRegistry()
	if err := reg.Register(a); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	if _, err := reg.Get(channel.KindCodeArts); err != nil {
		t.Fatalf("取用失败: %v", err)
	}
}

func TestAdapterModelsUsesBuiltinEndpointAndCaches(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != EpModelBuiltin {
			t.Errorf("path = %s, want %s", r.URL.Path, EpModelBuiltin)
		}
		if r.Header.Get("Agent-Type") != AgentType {
			t.Errorf("Agent-Type = %q", r.Header.Get("Agent-Type"))
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "SDK-HMAC-SHA256") {
			t.Error("目录请求也必须签名")
		}
		_, _ = w.Write([]byte(`{"builtinModels":[
			{"model_id":"deepseek-v3","model_name":"DeepSeek V3","context_window":128000,"max_tokens":8192},
			{"model_name":"only-name-model"}
		]}`))
	}))
	defer srv.Close()

	c := NewClient()
	c.snapHost = srv.URL
	a := NewAdapter(c, &fakeCreds{cred: SignCredential{AccessKeyID: "AK", SecretAccessKey: "SK"}}, 0)

	out, err := a.Models(context.Background())
	if err != nil {
		t.Fatalf("拉目录失败: %v", err)
	}
	if len(out) != 2 || out[0].ID != "deepseek-v3" || out[1].ID != "only-name-model" {
		t.Fatalf("目录解析不对: %+v", out)
	}
	if out[0].CostKnown {
		t.Error("码道没有单价，CostKnown 必须为 false")
	}
	// 第二次应命中缓存
	if _, err := a.Models(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("目录应缓存，实际请求 %d 次", hits)
	}
}

func TestAdapterChatRewritesFrames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	c := NewClient()
	c.chatEndpointOverride = srv.URL
	a := NewAdapter(c, &fakeCreds{cred: SignCredential{AccessKeyID: "AK", SecretAccessKey: "SK", SecurityToken: "ST"}}, 0)

	var buf bytes.Buffer
	if err := a.Chat(context.Background(), "acc-1", "deepseek-v3", []byte(`{"messages":[]}`), &buf); err != nil {
		t.Fatalf("chat 失败: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, `data: {"choices":[{"delta":{"content":"你"}}]}`) {
		t.Errorf("事件被改写坏了: %s", got)
	}
	if !strings.Contains(got, "data: [DONE]") {
		t.Error("缺少 [DONE] 结束帧")
	}
	if strings.Contains(got, ": heartbeat") {
		t.Error("心跳注释不应透传给下游")
	}
}

func TestAdapterChatCredentialError(t *testing.T) {
	a := NewAdapter(nil, &fakeCreds{err: errors.New("no credentials")}, 0)
	err := a.Chat(context.Background(), "acc", "m", []byte(`{}`), io.Discard)
	if err == nil {
		t.Fatal("取凭据失败应直接返回错误")
	}
}

func TestAdapterChatUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer srv.Close()

	c := NewClient()
	c.chatEndpointOverride = srv.URL
	a := NewAdapter(c, &fakeCreds{cred: SignCredential{AccessKeyID: "AK", SecretAccessKey: "SK"}}, 0)
	if err := a.Chat(context.Background(), "acc", "m", []byte(`{}`), io.Discard); err == nil {
		t.Fatal("上游 403 应返回错误")
	}
}
