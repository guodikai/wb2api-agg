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
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
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
	// Store 凭据目录：用于把扩展平台账号合并进「账号池」。
	Store *creds.Store
	// ReqLog 请求事件日志（跨平台）。nil 时不记录。
	ReqLog *ReqLog
}

// Wrap 返回包好中间件的 handler，把它塞进 http.Server.Handler 即可。
func Wrap(next http.Handler, reg *channel.Registry) http.Handler {
	return &Mux{Next: next, Registry: reg, Panel: buildPanel(), Store: sharedStore(), ReqLog: sharedReqLog()}
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
	// 账号池合并：把码道等平台的账号塞进上游 overview 响应。
	if r.Method == http.MethodGet && r.URL.Path == "/panel/api/overview" && m.Store != nil {
		serveOverviewWithPlatforms(w, m.Next, r, m.Store)
		return
	}
	// 主面板「运行日志/请求记录」：合并扩展平台的请求事件。
	if r.Method == http.MethodGet && r.URL.Path == "/panel/api/request_logs" && m.ReqLog != nil {
		serveRequestLogsMerged(w, r, m.Next, m.ReqLog)
		return
	}
	// /v1/models：合并扩展平台模型（客户端才看得到 codearts:xxx）。
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" && m.Registry != nil {
		serveModelsWithPlatforms(w, m.Next, r, m.Registry)
		return
	}
	// 主面板 HTML 注入我们的扩展脚本（上游文件零改动）。
	if m.Panel != nil && r.Method == http.MethodGet &&
		(r.URL.Path == "/panel/" || r.URL.Path == "/panel/index.html") {
		injectPanelScript(w, m.Next, r)
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
	// 裸名（不含 cn:/global: 这类 realm 前缀）自动跨平台选路
	autoRouted := false
	if !target.Explicit && !strings.HasPrefix(req.Model, "cn:") && !strings.HasPrefix(req.Model, "global:") {
		if k, mdl, ok := m.autoPick(r.Context(), r, req.Model); ok {
			target = Target{Kind: k, Explicit: true, Model: mdl}
			autoRouted = true
		}
	}
	if !target.Explicit {
		restore()
		started := time.Now()
		rw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		m.Next.ServeHTTP(rw, r)
		m.logEvent(ReqEvent{
			Time: started, Platform: string(channel.KindWorkBuddy), Model: req.Model, ModelRaw: req.Model,
			Stream: req.Stream == nil || *req.Stream, Status: rw.code, OK: rw.code < 400,
			DurationMs: time.Since(started).Milliseconds(), BytesOut: rw.bytes,
			ClientIP: clientIP(r), Outcome: outcomeOf(rw.code),
		})
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
	started := time.Now()
	ev := ReqEvent{Time: started, Platform: string(target.Kind), Model: target.Model,
		ModelRaw: req.Model, Stream: stream, ClientIP: clientIP(r)}
	usage := &channel.Usage{}
	r = r.WithContext(channel.WithUsage(r.Context(), usage))
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		// 不提前 WriteHeader：让第一次 Write 隐式提交，这样自动选路失败时还能安全回退。
		flusher, _ := w.(http.Flusher)
		fw := &flushWriter{w: w, f: flusher}
		chatErr := adapter.Chat(r.Context(), "", target.Model, body, fw)
		if chatErr != nil && autoRouted && fw.n == 0 {
			// 一个字都没写出去 → 头未提交，回退给 WorkBuddy 原路重试
			for _, h := range []string{"Content-Type", "Cache-Control", "X-Accel-Buffering"} {
				w.Header().Del(h)
			}
			restore()
			m.Next.ServeHTTP(w, r)
			return
		}
		if chatErr != nil {
			// 头已发出，只能写一条错误事件收尾
			_, _ = fw.Write([]byte("data: " + errEvent(chatErr) + "\n\n"))
		}
		ev.Status, ev.OK, ev.Err = http.StatusOK, chatErr == nil, errText(chatErr)
		ev.Outcome = "stream"
		if chatErr != nil {
			ev.Outcome = "upstream_error"
		}
		ev.DurationMs, ev.BytesOut = time.Since(started).Milliseconds(), fw.n
		ev.PromptTokens, ev.OutputTokens, ev.Effort = usage.Prompt, usage.Completion, usage.Effort
		if usage.Model != "" && usage.Model != target.Model {
			ev.ModelEff = usage.Model
		}
		m.logEvent(ev)
		return
	}

	var buf bytes.Buffer
	if err := adapter.Chat(r.Context(), "", target.Model, body, &buf); err != nil && autoRouted {
		// 自动选路失败 → 静默回退（客户端只看到 WorkBuddy 的结果）
		restore()
		m.Next.ServeHTTP(w, r)
		return
	}
	content := collectSSEText(buf.String())
	if content == "" {
		writeJSONError(w, http.StatusBadGateway, "empty_response", "上游没有返回任何文本")
		return
	}
	ev.Status, ev.OK, ev.Outcome = http.StatusOK, true, "sync"
	ev.DurationMs, ev.BytesOut = time.Since(started).Milliseconds(), int64(buf.Len())
	ev.PromptTokens, ev.OutputTokens, ev.Effort = usage.Prompt, usage.Completion, usage.Effort
	if usage.Model != "" && usage.Model != target.Model {
		ev.ModelEff = usage.Model
	}
	m.logEvent(ev)
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
	n int64
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	fw.n += int64(n)
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


// captureWriter 记录响应，用于在主面板 HTML 里插入一行 script。
type captureWriter struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (c *captureWriter) Header() http.Header { return c.header }
func (c *captureWriter) WriteHeader(code int) { c.code = code }
func (c *captureWriter) Write(p []byte) (int, error) { return c.body.Write(p) }

// injectPanelScript 把 <script src="/panel/agg/panel.js"> 插到 </body> 前。
// 任何异常（非 200、非 HTML、找不到 </body>）都原样透传，绝不让主面板打不开。
func injectPanelScript(w http.ResponseWriter, next http.Handler, r *http.Request) {
	cw := &captureWriter{header: make(http.Header), code: http.StatusOK}
	next.ServeHTTP(cw, r)

	body := cw.body.Bytes()
	ct := cw.header.Get("Content-Type")
	if cw.code != http.StatusOK || !strings.Contains(strings.ToLower(ct), "text/html") ||
		!bytes.Contains(body, []byte("</body>")) {
		for k, vs := range cw.header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(cw.code)
		_, _ = w.Write(body)
		return
	}

	const tag = `<script src="/panel/agg/panel.js" defer></script>`
	out := bytes.Replace(body, []byte("</body>"), []byte(tag+"</body>"), 1)
	for k, vs := range cw.header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}
