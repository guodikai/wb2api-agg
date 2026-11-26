// oauth.go 华为云 STS 的 token 交换：授权码换 token / refresh_token 续期。
//
// 移植来源：HITZY2002/codearts2api internal/upstream/client.go（MIT）。
//
// 两条铁律：
//  1. 每次请求都要带 DPoP 证明，且必须是**签发该 refresh_token 用的那把私钥**；
//  2. refresh_token 一次性轮换——换回来的新 RT 必须立刻落盘，否则下次就废了。
package codearts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BuildAuthorizeURL 构造 portal 登录链接（PKCE + ticket 轮询）。
func (c *Client) BuildAuthorizeURL(cfg LoginConfig, ticketID, codeChallenge string, port int) string {
	q := url.Values{}
	q.Set("theme", "2")
	q.Set("locale", "zh-cn")
	q.Set("uri_scheme", cfg.ClientID)
	q.Set("client_id", cfg.ClientID)
	q.Set("port", fmt.Sprint(port))
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "SHA-256")
	q.Set("ticket_id", ticketID)
	q.Set("plugin-name", cfg.PluginName)
	q.Set("plugin-version", cfg.PluginVersion)
	return cfg.PortalHost + "/authorize?" + q.Encode()
}

// ExchangeCode 用授权码换 token。dpopJWK 为 nil 时随机生成（调用方必须持久化）。
func (c *Client) ExchangeCode(ctx context.Context, cfg LoginConfig, code, codeVerifier string, port int, dpopJWK DPoPPrivateJWK) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("code", code)
	form.Set("code_verifier", codeVerifier)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", fmt.Sprintf("http://127.0.0.1:%d%s", port, cfg.RedirectPath))
	kp, err := dpopKeyPairFor(dpopJWK)
	if err != nil {
		return nil, err
	}
	return c.requestToken(ctx, cfg, form, kp)
}

// RefreshToken 用 refresh_token 换新凭证（返回的新 RT 必须覆盖旧的）。
func (c *Client) RefreshToken(ctx context.Context, cfg LoginConfig, refreshToken, codeVerifier string, dpopJWK DPoPPrivateJWK) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("code_verifier", codeVerifier)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	kp, err := dpopKeyPairFor(dpopJWK)
	if err != nil {
		return nil, err
	}
	return c.requestToken(ctx, cfg, form, kp)
}

// requestToken 向 STS 令牌端点发 form + DPoP。
func (c *Client) requestToken(ctx context.Context, cfg LoginConfig, form url.Values, kp *dpopKeyPair) (*TokenResponse, error) {
	base := c.stsHost
	if cfg.STSHost != "" {
		base = cfg.STSHost
	}
	endpoint := base + EpOAuthTokens
	proof, err := signDPoPProofAt(kp, endpoint, nowUTC())
	if err != nil {
		return nil, fmt.Errorf("dpop proof: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("DPoP", proof)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	if resp.StatusCode >= 400 {
		var tokenErr TokenResponse
		_ = json.Unmarshal(raw, &tokenErr)
		// 刷新通道的终态失效单独识别：继续重试没有意义，必须重新登录。
		if form.Get("grant_type") == "refresh_token" && isTerminalRefreshFailure(tokenErr) {
			return nil, &RefreshTokenExpiredError{
				Status: resp.StatusCode,
				Message: fmt.Sprintf("refresh_token 已失效: http=%d error=%s code=%s msg=%s",
					resp.StatusCode, tokenErr.Error, tokenErr.ErrorCode, truncate(tokenErr.ErrorMessage, 160)),
			}
		}
		return nil, fmt.Errorf("token 端点 %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var out TokenResponse
	out.RawJSON = append([]byte(nil), raw...)
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析 token 响应失败: %w body=%s", err, truncate(string(raw), 200))
	}
	if err := out.Normalize(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, truncate(string(raw), 200))
	}
	return &out, nil
}

// nowUTC 便于测试替换（签名里的 iat 用）。
func nowUTC() time.Time { return time.Now().UTC() }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// dpopKeyPairFor 用给定私钥恢复密钥对；未提供（旧凭证）时随机生成。
func dpopKeyPairFor(jwk DPoPPrivateJWK) (*dpopKeyPair, error) {
	if len(jwk) > 0 && jwk["d"] != "" {
		return dpopKeyPairFromJWK(jwk)
	}
	fresh, err := NewDPoPPrivateJWK()
	if err != nil {
		return nil, fmt.Errorf("生成 DPoP 密钥失败: %w", err)
	}
	return dpopKeyPairFromJWK(fresh)
}
