// http.go 聚合入口：包在 misakano 现有 handler 外面的一层中间件。
//
// 为什么用中间件而不是新增 /agg 路径：客户端只认 /v1/chat/completions，
// 多一个路径等于让用户改配置。这里只在「模型名带通道前缀」时接管，
// 其余请求（含 cn: 与裸名）原样透传给上游——零回归的关键。
package agg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
)

// Mux 聚合中间件。
type Mux struct {
	// Next 上游（misakano）处理器。
	Next http.Handler
	// Registry 已注册的通道适配器。
	Registry *channel.Registry
	// MaxBody 请求体读取上限（0 = 64MB）。
	MaxBody int64
	// Panel 我们自己的小面板（/panel/agg/）。nil 时这些路径交回上游。
	Panel http.Handler
}

// Wrap 返回包好中间件的 handler，把它塞进 http.Server.Handler 即可。
func Wrap(next http.Handler, reg *channel.Registry) http.Handler {
	return &Mux{Next: next, Registry: reg, Panel: buildPanel()}
}

func (m *Mux) maxBody() int64 {
	if m.MaxBody > 0 {
		return m.MaxBody
	}
	return 64 << 20
}

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 我们自己的面板页：只认 /panel/agg/ 前缀，其余 /panel/* 交给上游。
	if m.Panel != nil && strings.HasPrefix(r.URL.Path, "/panel/agg") {
		m.Panel.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/v1/chat/completions") {
		m.Next.ServeHTTP(w, r)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, m.maxBody()))
	if err != nil {
		m.Next.ServeHTTP(w, r) // 读不出来就交回上游，让它的错误处理照旧
		return
	}
	_ = r.Body.Close()

	// 先还原 body，任何"透传"分支都必须能读到完整内容。
	restore := func() { r.Body = io.NopCloser(bytes.NewReader(body)) }

	var req struct {
		Model  string `json:"model"`
		Stream *bool  `json:"stream"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		restore()
		m.Next.ServeHTTP(w, r)
		return
	}

	target := SplitModel(req.Model)
	if !target.Explicit {
		restore()
		m.Next.ServeHTTP(w, r)
		return
	}

	adapter, aerr := m.Registry.Get(target.Kind)
	if aerr != nil {
		writeJSONError(w, http.StatusBadRequest, "channel_not_connected",
			"模型 %q 指定的平台 %q 尚未接入本网关（未配置账号或适配器未注册）", req.Model, string(target.Kind))
		return
	}

	// 适配器输出统一是 SSE；客户端要非流式时聚合后再回。
	stream := req.Stream == nil || *req.Stream
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fw := &flushWriter{w: w, f: flusher}
		if err := adapter.Chat(r.Context(), "", target.Model, body, fw); err != nil {
			// 头已发出，只能写一条错误事件收尾
			_, _ = fw.Write([]byte("data: " + errEvent(err) + "\n\n"))
		}
		return
	}

	var buf bytes.Buffer
	if err := adapter.Chat(r.Context(), "", target.Model, body, &buf); err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream_error", "%s", err.Error())
		return
	}
	content := collectSSEText(buf.String())
	if content == "" {
		writeJSONError(w, http.StatusBadGateway, "empty_response", "上游没有返回任何文本")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "chat.completion",
		"model":  req.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
	})
}

type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}
	return n, err
}

// collectSSEText 把 SSE 帧里的增量文本拼起来（非流式请求用）。
// 只取 choices[].delta.content / .message.content；工具调用等结构化增量暂不聚合
// （P2 阶段先支持文本，工具桥接在 P3 之后补）。
func collectSSEText(stream string) string {
	var sb strings.Builder
	for _, line := range strings.Split(stream, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &ev) != nil || len(ev.Choices) == 0 {
			continue
		}
		if c := ev.Choices[0].Delta.Content; c != "" {
			sb.WriteString(c)
			continue
		}
		sb.WriteString(ev.Choices[0].Message.Content)
	}
	return sb.String()
}

func errEvent(err error) string {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]any{"message": err.Error(), "type": "upstream_error"},
	})
	return string(b)
}

func writeJSONError(w http.ResponseWriter, status int, code, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": msg, "type": "invalid_request_error"},
	})
}
