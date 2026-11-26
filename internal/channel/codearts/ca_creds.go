// credentials.go 码道凭据模型与登录配置。
//
// 移植来源：HITZY2002/codearts2api internal/upstream/client.go（MIT）的相关类型。
//
// 关键约束（踩过就知道）：
//   - refresh_token 绑定 DPoP 公钥，私钥必须持久化复用，否则刷新 InvalidDPoPHeader
//   - refresh_token 一次性轮换 → 同一账号只允许一个刷新者（我们的消费侧）
package codearts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"
)

// LoginConfig 登录端点配置。生产用 DefaultLoginConfig，测试可替换主机。
type LoginConfig struct {
	ClientID      string
	PortalHost    string
	SnapManager   string
	STSHost       string
	RedirectPath  string
	PluginName    string
	PluginVersion string
}

// DefaultLoginConfig 生产默认值（逆向自 huaweicloud.authentication 扩展）。
func DefaultLoginConfig() LoginConfig {
	return LoginConfig{
		ClientID:      ClientID,
		PortalHost:    PortalHost,
		SnapManager:   SnapManagerHost,
		STSHost:       STSHost,
		RedirectPath:  "/oauth/callback",
		PluginName:    "snap_AIIDE",
		PluginVersion: "5.2.0",
	}
}

// Credentials STS 下发的临时 AK/SK（chat 请求签名的凭据）。
type Credentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SecurityToken   string `json:"security_token"`
	Expiration      string `json:"expiration"`
}

// LegacyCredential ticket 通道（旧登录）返回的凭证字段。
type LegacyCredential struct {
	Access        string `json:"access"`
	Secret        string `json:"secret"`
	SecurityToken string `json:"securitytoken"`
	ExpiresAt     string `json:"expires_at"`
}

// TokenResponse oauth2/tokens 的响应。
type TokenResponse struct {
	UserID       string           `json:"user_id"`
	UserName     string           `json:"user_name"`
	DomainID     string           `json:"domain_id"`
	RefreshToken string           `json:"refresh_token"`
	Credentials  Credentials      `json:"credentials"`
	Credential   LegacyCredential `json:"credential"`
	// 错误字段（refresh 失败时上游会返回）。
	Error        string `json:"error"`
	ErrorCode    string `json:"error_code"`
	ErrorMessage string `json:"error_msg"`
}

// Normalize 把 legacy 字段折叠到 Credentials，并校验凭据完整性。
func (t *TokenResponse) Normalize() error {
	if t.Credentials.SecurityToken == "" && t.Credential.SecurityToken != "" {
		t.Credentials = Credentials{
			AccessKeyID:     t.Credential.Access,
			SecretAccessKey: t.Credential.Secret,
			SecurityToken:   t.Credential.SecurityToken,
			Expiration:      t.Credential.ExpiresAt,
		}
	}
	if t.Credentials.SecurityToken == "" || t.Credentials.AccessKeyID == "" {
		return fmt.Errorf("token 响应缺少 STS 凭据")
	}
	return nil
}

// RefreshTokenExpiredError 续期凭据终态失效，只能重新登录；
// 与网络/5xx 等可重试错误分开，避免无限刷新。
type RefreshTokenExpiredError struct {
	Status  int
	Message string
}

func (e *RefreshTokenExpiredError) Error() string { return e.Message }

// isTerminalRefreshFailure 判定上游错误是否属于"刷新通道终态失效"。
func isTerminalRefreshFailure(t TokenResponse) bool {
	if t.Error == "invalid_grant" {
		return true
	}
	for _, m := range []string{"ExpiredRefreshToken", "InvalidDPoPHeader", "invalid client id"} {
		if containsFold(t.ErrorCode, m) {
			return true
		}
	}
	return containsFold(t.ErrorMessage, "has been used")
}

// Client 码道云 API 客户端（token 交换用短超时；chat 走 streamHTTP）。
type Client struct {
	http       *http.Client
	streamHTTP *http.Client
	stsHost    string
	snapHost   string
	portalHost string
	// chatEndpointOverride 仅测试用：覆盖 chat 端点（httptest）。
	chatEndpointOverride string
}

// NewClient 生产默认客户端。
func NewClient() *Client {
	cfg := DefaultLoginConfig()
	return &Client{
		http:       &http.Client{Timeout: 30 * time.Second},
		streamHTTP: &http.Client{Timeout: 0},
		stsHost:    cfg.STSHost,
		snapHost:   cfg.SnapManager,
		portalHost: cfg.PortalHost,
	}
}

// PKCE code_verifier / code_challenge（S256）。
type PKCE struct {
	Verifier  string
	Challenge string
	Method    string
}

// NewPKCE 生成一对 PKCE（verifier 43 字节随机 → base64url 无填充）。
func NewPKCE() (PKCE, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return PKCE{}, err
	}
	v := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(v))
	return PKCE{
		Verifier:  v,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		Method:    "SHA-256",
	}, nil
}

func containsFold(s, sub string) bool {
	if sub == "" {
		return false
	}
	return len(s) >= len(sub) && indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	ls, lsub := lower(s), lower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
