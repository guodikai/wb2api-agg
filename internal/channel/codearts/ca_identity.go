// identity.go 登录后取账号身份：三级兜底，与参照实现一致。
//
// 为什么要三级（HITZY internal/server/handler.go 的顺序，实测最稳）：
//  1. CallerIdentity（/v5/caller-identity）——最权威，带 principal/account
//  2. CurrentUser（/v1/current/user）——次之
//  3. refresh_token 是 JWT，载荷里的 sub 就是 user id —— 纯本地兜底
//
// 任何一级成功即返回；全部失败才算登录失败（错误里带上尝试记录）。
package codearts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Identity 账号身份。
type Identity struct {
	UserID   string
	UserName string
	DomainID string
	// Via 哪一级拿到的（caller-identity / current-user / refresh-token），便于排障。
	Via string
}

// CallerIdentity 用 STS 凭据查调用者身份（/v5/caller-identity）。
func (c *Client) CallerIdentity(ctx context.Context, cred SignCredential, endpoint string) (Identity, error) {
	if endpoint == "" {
		endpoint = c.snapHost + EpCallerIdentity
	}
	raw, err := c.signedGet(ctx, endpoint, cred)
	if err != nil {
		return Identity{}, err
	}
	var out struct {
		PrincipalID  string `json:"principal_id"`
		PrincipalURN string `json:"principal_urn"`
		AccountID    string `json:"account_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Identity{}, fmt.Errorf("解析 caller-identity 失败: %w", err)
	}
	name := out.PrincipalURN
	// principal_urn 形如 iam::<account>:user:<name>，取最后一段当显示名
	if i := strings.LastIndex(name, ":user:"); i >= 0 {
		name = name[i+len(":user:"):]
	}
	if out.PrincipalID == "" {
		return Identity{}, fmt.Errorf("caller-identity 没有 principal_id")
	}
	return Identity{UserID: out.PrincipalID, UserName: name, DomainID: out.AccountID, Via: "caller-identity"}, nil
}

func (c *Client) signedGet(ctx context.Context, endpoint string, cred SignCredential) ([]byte, error) {
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
		return nil, fmt.Errorf("上游 %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	return raw, nil
}

// IdentityFromRefreshToken 从 JWT 形式的 refresh_token 里取 sub（纯本地，无网络）。
func IdentityFromRefreshToken(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// 有些实现用标准 base64（带填充），试一次
		if raw, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return ""
		}
	}
	var payload struct {
		Sub      string `json:"sub"`
		UserID   string `json:"user_id"`
		UserName string `json:"user_name"`
		UniqueID string `json:"unique_id"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	for _, v := range []string{payload.Sub, payload.UserID, payload.UniqueID, payload.UserName} {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ResolveIdentity 三级兜底取身份。
func (c *Client) ResolveIdentity(ctx context.Context, cred SignCredential, refreshToken string) (Identity, error) {
	var attempts []string

	if id, err := c.CallerIdentity(ctx, cred, ""); err == nil && id.UserID != "" {
		return id, nil
	} else {
		attempts = append(attempts, fmt.Sprintf("caller-identity: %v", err))
	}

	if info, err := c.FetchCurrentUser(ctx, cred, ""); err == nil {
		id := Identity{
			UserID:   pickString(info, "user_id", "userId", "id", "uid"),
			UserName: pickString(info, "user_name", "userName", "name"),
			DomainID: pickString(info, "domain_id", "domainId", "account_id"),
			Via:      "current-user",
		}
		if id.UserID != "" {
			return id, nil
		}
		attempts = append(attempts, "current-user: 响应无 user_id")
	} else {
		attempts = append(attempts, fmt.Sprintf("current-user: %v", err))
	}

	if sub := IdentityFromRefreshToken(refreshToken); sub != "" {
		return Identity{UserID: sub, Via: "refresh-token"}, nil
	}
	attempts = append(attempts, "refresh-token: 载荷无 sub")

	return Identity{}, fmt.Errorf("无法确定账号身份（%s）", strings.Join(attempts, "；"))
}

func pickString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, obj := range []string{"user", "principal", "account", "profile"} {
		if sub, ok := m[obj].(map[string]any); ok {
			if v := pickString(sub, keys...); v != "" {
				return v
			}
		}
	}
	return ""
}
