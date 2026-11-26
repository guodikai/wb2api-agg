// user.go 登录后补取身份信息（/v1/current/user）。
//
// 为什么需要：真实 STS 的 authorization_code 响应里不一定带 user_id（HITZY 的
// 结构假定它在顶层），缺了就没法给账号起名。这时用刚拿到的 STS 凭据签一次
// current/user 就能补上——这也是官方插件登录后做的第一件事。
package codearts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// FetchCurrentUser 返回上游的用户信息原始对象（字段名各版本可能不同，按需取）。
func (c *Client) FetchCurrentUser(ctx context.Context, cred SignCredential, endpoint string) (map[string]any, error) {
	if endpoint == "" {
		endpoint = c.snapHost + EpCurrentUser
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HdrAgentType, AgentType)
	req.Header.Set(HdrLanguage, LangZHCN)
	if cred.SecurityToken != "" {
		req.Header.Set("X-Security-Token", cred.SecurityToken)
	}
	SignRequest(req, []byte{}, cred)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, &UpstreamError{Status: resp.StatusCode, Message: truncate(string(raw), 200), Path: EpCurrentUser}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
