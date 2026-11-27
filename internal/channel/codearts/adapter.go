// adapter.go 薄转接层：把"照抄进来的参照实现"接到网关的 channel.Adapter 接口上。
//
// 这里不实现任何协议逻辑——协议全在同目录的 client.go/models.go/signer.go/dpop.go/sse.go
// 里，原样照抄（只改包名与 import 路径）。本文件只做三件事：
//   1. 把我们的凭据记录转成参照实现的 auth.Auth
//   2. 把 OpenA I请求体转成它的 ChatMessage
//   3. 把它的 SSE 流原样写到调用方
package codearts

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts/auth"
)

// CredentialSource 凭据来源（由 creds 包实现）。
type CredentialSource interface {
	// Any 返回任意可用账号（平台内选号；P4 接统一调度后由上层指定具体账号）。
	Any(ctx context.Context) (uid string, acct *auth.Auth, err error)
	// For 返回指定账号。
	For(ctx context.Context, uid string) (*auth.Auth, error)
}

// Adapter 码道通道适配器。
type Adapter struct {
	client *Client
	creds  CredentialSource
}

// NewAdapter 构造。client 为 nil 时用参照实现的默认构造（120s 超时）。
func NewAdapter(c *Client, src CredentialSource) *Adapter {
	if c == nil {
		c = New(0)
	}
	return &Adapter{client: c, creds: src}
}

// Kind 实现 channel.Adapter。
func (a *Adapter) Kind() channel.Kind { return channel.KindCodeArts }

// Models 实现 channel.Adapter。码道没有单价，CostKnown 恒为 false。
func (a *Adapter) Models(ctx context.Context) ([]channel.ModelInfo, error) {
	_, acct, err := a.creds.Any(ctx)
	if err != nil {
		return nil, err
	}
	infos, err := a.client.FetchModels(acct)
	if err != nil {
		return nil, err
	}
	out := make([]channel.ModelInfo, 0, len(infos))
	for _, m := range infos {
		out = append(out, channel.ModelInfo{ID: m.ID, CostKnown: false})
	}
	return out, nil
}

// Chat 实现 channel.Adapter：上游 SSE 原样透传（不重新解析、不重新拼帧）。
func (a *Adapter) Chat(ctx context.Context, accountID, model string, body []byte, w io.Writer) error {
	var acct *auth.Auth
	var err error
	if strings.TrimSpace(accountID) == "" {
		_, acct, err = a.creds.Any(ctx)
	} else {
		acct, err = a.creds.For(ctx, accountID)
	}
	if err != nil {
		return err
	}

	var req struct {
		Messages  []ChatMessage `json:"messages"`
		ChatID    string        `json:"chat_id"`
		SessionID string        `json:"session_id"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return err
		}
	}

	// 把实际使用的账号上报给中间件，请求日志才能显示"平台 + 账号"
	channel.ReportUsage(ctx, channel.Usage{Account: acct.UserName})

	cred := SignCredential{
		AccessKeyID:     acct.AccessKeyID,
		SecretAccessKey: acct.SecretAccessKey,
		SecurityToken:   acct.CloudDragonTok,
	}
	rc, err := a.client.ChatStream(ctx, req.ChatID, req.Messages, "", cred, acct.UserName, model, false)
	if err != nil {
		return err
	}
	defer rc.Close()
	// 逐行透传 + 解析 usage（io.Copy 拿不到 token 用量）
	return copySSEWithUsage(ctx, w, rc, model)
}
