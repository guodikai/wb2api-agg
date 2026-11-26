package agg

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
)

type fakeAdapter struct {
	gotModel string
	gotBody  []byte
	frames   []string
	err      error
}

func (f *fakeAdapter) Kind() channel.Kind { return channel.KindCodeArts }
func (f *fakeAdapter) Models(context.Context) ([]channel.ModelInfo, error) {
	return []channel.ModelInfo{{ID: "deepseek-v3"}}, nil
}
func (f *fakeAdapter) Chat(_ context.Context, _ string, model string, body []byte, w io.Writer) error {
	f.gotModel = model
	f.gotBody = body
	if f.err != nil {
		return f.err
	}
	for _, fr := range f.frames {
		if _, err := w.Write([]byte("data: " + fr + "\n\n")); err != nil {
			return err
		}
	}
	_, err := w.Write([]byte("data: [DONE]\n\n"))
	return err
}

func newMux(t *testing.T, ad *fakeAdapter) (*Mux, *http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	reg := channel.NewRegistry()
	if ad != nil {
		if err := reg.Register(ad); err != nil {
			t.Fatal(err)
		}
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Upstream-Body", string(b))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"from":"upstream"}`))
	})
	return &Mux{Next: next, Registry: reg}, nil, httptest.NewRecorder()
}

func TestMuxPassesThroughWorkBuddyModels(t *testing.T) {
	for _, model := range []string{"cn:deepseek-v4-flash", "hy4flash-free", "qwen:max"} {
		m, _, rec := newMux(t, nil)
		body := `{"model":"` + model + `","messages":[]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		m.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "upstream") {
			t.Fatalf("%s 应透传给上游，实际 %d %s", model, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Upstream-Body"); got != body {
			t.Errorf("%s 透传时 body 必须完整，实际 %q", model, got)
		}
	}
}

func TestMuxNonChatPathPassesThrough(t *testing.T) {
	m, _, rec := newMux(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "upstream") {
		t.Fatalf("GET /v1/models 应透传，实际 %d", rec.Code)
	}
}

func TestMuxUnregisteredChannelIsClearError(t *testing.T) {
	m, _, rec := newMux(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"codearts:deepseek-v3","messages":[]}`))
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未接入平台应 400，实际 %d", rec.Code)
	}
	var got struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Error.Code != "channel_not_connected" {
		t.Errorf("错误码 = %q", got.Error.Code)
	}
	if !strings.Contains(got.Error.Message, "codearts") {
		t.Errorf("错误信息应点名平台: %q", got.Error.Message)
	}
}

func TestMuxRoutesToAdapterAndStripsPrefix(t *testing.T) {
	ad := &fakeAdapter{frames: []string{`{"choices":[{"delta":{"content":"你"}}]}`}}
	m, _, rec := newMux(t, ad)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"codearts:deepseek-v3","messages":[],"stream":true}`))
	m.ServeHTTP(rec, req)

	if ad.gotModel != "deepseek-v3" {
		t.Errorf("适配器应拿到剥掉前缀的 model，实际 %q", ad.gotModel)
	}
	if !strings.Contains(string(ad.gotBody), "messages") {
		t.Error("适配器应拿到完整原始 body")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q", ct)
	}
	out := rec.Body.String()
	if !strings.Contains(out, `"content":"你"`) || !strings.Contains(out, "[DONE]") {
		t.Errorf("SSE 输出不对: %s", out)
	}
}

func TestMuxNonStreamAggregates(t *testing.T) {
	ad := &fakeAdapter{frames: []string{
		`{"choices":[{"delta":{"content":"你"}}]}`,
		`{"choices":[{"delta":{"content":"好"}}]}`,
	}}
	m, _, rec := newMux(t, ad)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"codearts:deepseek-v3","messages":[],"stream":false}`))
	m.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("非流式应回 JSON，实际 %q", ct)
	}
	var got struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("响应不是 JSON: %s", rec.Body.String())
	}
	if len(got.Choices) == 0 || got.Choices[0].Message.Content != "你好" {
		t.Errorf("聚合文本不对: %+v", got)
	}
}

func TestMuxAdapterErrorNonStream(t *testing.T) {
	ad := &fakeAdapter{err: io.ErrUnexpectedEOF}
	m, _, rec := newMux(t, ad)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"codearts:m","messages":[],"stream":false}`))
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("适配器失败应 502，实际 %d", rec.Code)
	}
}
