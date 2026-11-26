package codearts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sseBody = ": heartbeat\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n" +
	"data: {\"choices\":[],\"usage\":{\"total_tokens\":3}}\n\n" +
	"data: [DONE]\n\n"

func TestChatStreamSendsHeadersSignsAndReadsSSE(t *testing.T) {
	var gotAuth, gotToken, gotAppID, gotChatID, gotSessionID, gotMaa string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotToken = r.Header.Get("x-auth-token")
		gotAppID = r.Header.Get("app-id")
		gotChatID = r.Header.Get("Chat-Id")
		gotSessionID = r.Header.Get("Session-Id")
		gotMaa = r.Header.Get("maas_type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseBody))
	}))
	defer srv.Close()

	c := NewClient()
	cred := SignCredential{AccessKeyID: "AK", SecretAccessKey: "SK", SecurityToken: "ST"}
	body := []byte(`{"model":"ignored","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	rc, err := c.ChatStream(context.Background(), "deepseek-v3", body, cred, "USER_TOK", ChatOptions{
		TraceID: "trace-1", ChatID: "chat-1", SessionID: "sess-1", Benefit: true, Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatalf("chat 失败: %v", err)
	}
	defer rc.Close()

	var text strings.Builder
	err = ReadSSE(rc, func(d []byte) error {
		text.WriteString(ExtractDelta(d))
		return nil
	})
	if err != nil && err != io.EOF {
		t.Fatalf("读 SSE 失败: %v", err)
	}
	if text.String() != "你好" {
		t.Errorf("增量文本 = %q, want 你好", text.String())
	}

	// 请求侧断言
	if !strings.HasPrefix(gotAuth, "SDK-HMAC-SHA256 Access=AK") {
		t.Errorf("缺少 AK/SK 签名: %q", gotAuth)
	}
	if !strings.Contains(gotAuth, "x-auth-token") {
		t.Error("签名必须覆盖 x-auth-token")
	}
	if gotToken != "ST" {
		t.Errorf("x-auth-token 应为 security_token，实际 %q", gotToken)
	}
	if gotAppID != "CodeAgent3.0" {
		t.Errorf("app-id = %q", gotAppID)
	}
	if gotChatID != "chat-1" || gotSessionID != "sess-1" {
		t.Errorf("亲和头丢失: chat=%q session=%q", gotChatID, gotSessionID)
	}
	if gotMaa != "benefit" {
		t.Errorf("福利模型必须带 maas_type: benefit，实际 %q", gotMaa)
	}
	if gotBody["model"] != "deepseek-v3" {
		t.Errorf("model 应被覆盖为上游 id，实际 %v", gotBody["model"])
	}
}

func TestChatStreamUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"signature mismatch"}`))
	}))
	defer srv.Close()

	c := NewClient()
	_, err := c.ChatStream(context.Background(), "m", []byte(`{}`),
		SignCredential{AccessKeyID: "AK", SecretAccessKey: "SK"}, "T", ChatOptions{Endpoint: srv.URL})
	if err == nil {
		t.Fatal("401 应报错")
	}
	ue, ok := err.(*UpstreamError)
	if !ok || ue.Status != http.StatusUnauthorized {
		t.Fatalf("错误类型/状态不对: %#v", err)
	}
}

func TestChatStreamRejectsBadJSON(t *testing.T) {
	c := NewClient()
	if _, err := c.ChatStream(context.Background(), "m", []byte(`{oops`),
		SignCredential{}, "T", ChatOptions{}); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

func TestReadSSEStopsAtDoneAndIgnoresHeartbeat(t *testing.T) {
	var events []string
	err := ReadSSE(strings.NewReader(sseBody), func(d []byte) error {
		events = append(events, string(d))
		return nil
	})
	if err != io.EOF {
		t.Fatalf("[DONE] 应返回 io.EOF，实际 %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("应收到 3 条事件（心跳被忽略），实际 %d: %v", len(events), events)
	}
}

func TestReadSSEMultilineData(t *testing.T) {
	stream := "data: {\"choices\":\ndata: []}\n\n"
	var got string
	_ = ReadSSE(strings.NewReader(stream), func(d []byte) error {
		got = string(d)
		return nil
	})
	if got != "{\"choices\":\n[]}" {
		t.Errorf("多行 data 应拼接，实际 %q", got)
	}
}
