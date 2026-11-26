// adapter_sse.go 把上游 SSE 逐行透传，同时解析 usage / 实际模型 / 档位并上报。
//
// 之前是 io.Copy 一把梭：最简单，但拿不到 token 用量与上游实际模型，导致面板的
// 请求日志只能记耗时。这里换成逐行扫描：字节原样写给客户端，边解析边 report。
package codearts

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
)

// copySSEWithUsage 逐行透传 SSE，并解析 usage 帧。
func copySSEWithUsage(ctx context.Context, dst io.Writer, src io.Reader, requestedModel string) error {
	br := bufio.NewReaderSize(src, 32<<10)
	var line bytes.Buffer
	for {
		chunk, err := br.ReadSlice('\n')
		line.Write(chunk)
		// ReadSlice 返回 ErrBufferFull 表示这一行超长，继续读（不改内容）
		if err == bufio.ErrBufferFull {
			continue
		}
		if line.Len() > 0 {
			raw := line.Bytes()
			if _, werr := dst.Write(raw); werr != nil {
				return werr
			}
			parseUsageLine(ctx, raw, requestedModel)
			line.Reset()
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// parseUsageLine 从 "data: {...}" 里取 usage / model / reasoning_effort。
func parseUsageLine(ctx context.Context, line []byte, requestedModel string) {
	s := strings.TrimSpace(string(line))
	if !strings.HasPrefix(s, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(s, "data:"))
	if payload == "" || payload == "[DONE]" {
		return
	}
	var chunk struct {
		Model   string `json:"model"`
		Usage   *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
		Choices []struct {
			Delta struct {
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal([]byte(payload), &chunk) != nil {
		return
	}
	u := channel.Usage{Model: chunk.Model}
	if u.Model == "" {
		u.Model = requestedModel
	}
	if chunk.Usage != nil {
		u.Prompt = chunk.Usage.PromptTokens
		u.Completion = chunk.Usage.CompletionTokens
	}
	// 有 reasoning_content 说明走的是思考档，记下来便于面板区分
	for _, c := range chunk.Choices {
		if c.Delta.ReasoningContent != "" {
			u.Effort = "thinking"
			break
		}
	}
	channel.ReportUsage(ctx, u)
}
