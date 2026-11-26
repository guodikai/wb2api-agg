// chat.go 码道 chat（/api/v2/chat/completions）+ SSE 读取。
//
// 移植来源：HITZY2002/codearts2api internal/upstream/{client.go,sse.go}（MIT）。
//
// 与 WorkBuddy 最大的不同：请求要 **AK/SK 签名**（signRequest），同时还要带
// 用户态 x-auth-token（= STS security_token）。两者缺一都被拒：
//   - 缺签名         → 401 签名不符
//   - 缺 x-auth-token→ 401 未登录
package codearts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChatHeaders 构造 chat 请求头（不含签名头，签名由 SignRequest 追加）。
func ChatHeaders(token, traceID, language string) map[string]string {
	if traceID == "" {
		traceID = fmt.Sprintf("%x", time.Now().UnixNano())
	}
	if language == "" {
		language = "zh-cn"
	}
	return map[string]string{
		"Content-Type":    "application/json",
		"Accept":          "text/event-stream",
		"x-auth-token":    token,
		"x-snap-traceid":  traceID,
		"X-Language":      language,
		"app-id":          "CodeAgent3.0",
		"is_confidential": "false",
	}
}

// UpstreamError 上游 HTTP 错误（非 2xx）。
type UpstreamError struct {
	Status  int
	Message string
	Path    string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("codearts upstream %d (%s): %s", e.Status, e.Path, e.Message)
}

// ChatOptions chat 的附加控制。
type ChatOptions struct {
	TraceID   string
	ChatID    string
	SessionID string
	// Benefit 限时福利模型必须带 maas_type: benefit（按账号判定，调用方负责）。
	Benefit bool
	// Endpoint 覆盖默认端点（测试用）。
	Endpoint string
}

// ChatStream 发一次 chat，返回 SSE 流（调用方负责 Close）。
//
// body 为 OpenAI 兼容的请求体原始 JSON；model 会覆盖其中 model 字段。
// x-auth-token 固定取 cred.SecurityToken——与官方插件一致（HITZY 的 sendChatV2
// 也传 cred.SecurityToken）。签名与用户态令牌是两套东西，缺一都 401。
func (c *Client) ChatStream(ctx context.Context, model string, body []byte, cred SignCredential, opt ChatOptions) (io.ReadCloser, error) {
	payload := map[string]any{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("请求体不是合法 JSON: %w", err)
		}
	}
	payload["model"] = model

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := opt.Endpoint
	if endpoint == "" {
		endpoint = SnapEngineApiHost + EpChatV2
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	for k, v := range ChatHeaders(cred.SecurityToken, opt.TraceID, LangZHCN) {
		req.Header.Set(k, v)
	}
	if opt.Benefit {
		req.Header.Set("maas_type", "benefit")
	}
	// 签名必须覆盖全部头（含 maas_type），所以放在最后。
	SignRequest(req, raw, cred)
	if opt.ChatID != "" {
		req.Header.Set("Chat-Id", opt.ChatID)
	}
	if opt.SessionID != "" {
		req.Header.Set("Session-Id", opt.SessionID)
	}

	httpClient := c.streamHTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		return nil, &UpstreamError{Status: resp.StatusCode, Message: truncate(string(rawBody), 200), Path: EpChatV2}
	}
	return resp.Body, nil
}

// SSEEventHandler 处理一条 SSE 事件的数据部分（已去掉 "data: " 前缀）。
// 返回 io.EOF 表示停止读取（例如收到 [DONE]）。
type SSEEventHandler func(data []byte) error

// ReadSSE 逐条解析 SSE 流。
//
// 只做码道需要的部分：按行读、跳过注释/空行、累积多行 data、遇到 [DONE] 结束。
// 不做事件重放/断线续传——那是上层的事。
func ReadSSE(r io.Reader, handle SSEEventHandler) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	var data []byte
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		d := data
		data = nil
		if string(d) == "[DONE]" {
			return io.EOF
		}
		return handle(d)
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
			// 注释/心跳，忽略
		case strings.HasPrefix(line, "data:"):
			part := strings.TrimPrefix(line, "data:")
			part = strings.TrimPrefix(part, " ")
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, part...)
			// 兼容服务端不补空行直接 [DONE]
			if string(data) == "[DONE]" {
				return io.EOF
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

// ExtractDelta 从一条码道的 chat 事件里取增量文本（OpenAI 兼容形状）。
// 取不到（心跳/usage 帧）时返回空串，不报错。
func ExtractDelta(data []byte) string {
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
	if err := json.Unmarshal(data, &ev); err != nil || len(ev.Choices) == 0 {
		return ""
	}
	if c := ev.Choices[0].Delta.Content; c != "" {
		return c
	}
	return ev.Choices[0].Message.Content
}
