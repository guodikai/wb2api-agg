// codearts.go 码道凭据来源：文件存储 + 到期前自动刷新 + 单一写者。
//
// 码道的 refresh_token 是**一次性轮换**的：每刷新一次，旧 RT 立刻作废。
// 所以这里必须做到「同一账号同一时刻只有一个刷新动作」——用文件锁 + 锁内重读来保证。
package creds

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts"
)

// PlatformCodeArts 码道在凭据目录里的子目录名。
const PlatformCodeArts = "codearts"

// CodeArtsRecord 一个码道账号的持久化凭据。
type CodeArtsRecord struct {
	UserID     string `json:"user_id"`
	UserName   string `json:"user_name"`
	DomainID   string `json:"domain_id"`
	RefreshTok string `json:"refresh_token"`
	Verifier   string `json:"code_verifier"`
	// DPoPJWK 必须与 refresh_token 配对保存：换钥匙刷新会被 STS 拒。
	DPoPJWK         map[string]string `json:"dpop_jwk"`
	AccessKeyID     string            `json:"access_key_id"`
	SecretAccessKey string            `json:"secret_access_key"`
	SecurityToken   string            `json:"security_token"`
	// Expiration STS 凭据到期时间（RFC3339，上游给的就是字符串）。
	Expiration string `json:"expiration"`
	UpdatedAt  string `json:"updated_at"`
}

// expiresWithin 判断 STS 凭据是否即将过期（解析失败按"已过期"处理，强制刷新）。
func (r *CodeArtsRecord) expiresWithin(d time.Duration, now time.Time) bool {
	if r.SecurityToken == "" || r.AccessKeyID == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, r.Expiration)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05.000Z", r.Expiration)
		if err != nil {
			return true
		}
	}
	return now.Add(d).After(t)
}

func (r *CodeArtsRecord) signCredential() codearts.SignCredential {
	return codearts.SignCredential{
		AccessKeyID:     r.AccessKeyID,
		SecretAccessKey: r.SecretAccessKey,
		SecurityToken:   r.SecurityToken,
	}
}

// TokenRefresher 刷新能力（生产是 *codearts.Client，测试可注入假实现）。
type TokenRefresher interface {
	RefreshToken(ctx context.Context, cfg codearts.LoginConfig, refreshToken, codeVerifier string, jwk codearts.DPoPPrivateJWK) (*codearts.TokenResponse, error)
}

// CodeArtsSource 实现 codearts.CredentialSource。
type CodeArtsSource struct {
	Store         *Store
	Client        TokenRefresher
	Cfg           codearts.LoginConfig
	Now           func() time.Time
	RefreshWindow time.Duration // 默认 10 分钟
}

// NewCodeArtsSource 构造。
func NewCodeArtsSource(st *Store, c TokenRefresher, cfg codearts.LoginConfig) *CodeArtsSource {
	return &CodeArtsSource{Store: st, Client: c, Cfg: cfg, Now: time.Now, RefreshWindow: 10 * time.Minute}
}

func (s *CodeArtsSource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *CodeArtsSource) window() time.Duration {
	if s.RefreshWindow > 0 {
		return s.RefreshWindow
	}
	return 10 * time.Minute
}

// Any 返回第一个可用账号（目录探测用）。
func (s *CodeArtsSource) Any(ctx context.Context) (string, codearts.SignCredential, error) {
	ids, err := s.Store.List(PlatformCodeArts)
	if err != nil {
		return "", codearts.SignCredential{}, err
	}
	if len(ids) == 0 {
		return "", codearts.SignCredential{}, fmt.Errorf("没有已登录的码道账号")
	}
	for _, id := range ids {
		cred, err := s.For(ctx, id)
		if err == nil {
			return id, cred, nil
		}
	}
	return "", codearts.SignCredential{}, fmt.Errorf("码道账号均不可用（%d 个）", len(ids))
}

// For 返回指定账号的 STS 凭据；临近到期先刷新。
func (s *CodeArtsSource) For(ctx context.Context, accountID string) (codearts.SignCredential, error) {
	var rec CodeArtsRecord
	if err := s.Store.Load(PlatformCodeArts, accountID, &rec); err != nil {
		if os.IsNotExist(err) {
			return codearts.SignCredential{}, fmt.Errorf("码道账号 %s 不存在", accountID)
		}
		return codearts.SignCredential{}, err
	}
	if !rec.expiresWithin(s.window(), s.now()) {
		return rec.signCredential(), nil
	}
	if rec.RefreshTok == "" || rec.DPoPJWK == nil {
		return codearts.SignCredential{}, fmt.Errorf("码道账号 %s 缺少 refresh_token/DPoP 私钥，需要重新登录", accountID)
	}
	return s.refresh(ctx, accountID)
}

// refresh 在文件锁内完成「重读 → 判断 → 刷新 → 落盘」。
//
// 锁内重读是关键：等锁期间可能已有别的协程/进程刷过，此时直接用新值，
// 绝不重复消费那个一次性 refresh_token。
func (s *CodeArtsSource) refresh(ctx context.Context, accountID string) (codearts.SignCredential, error) {
	var result codearts.SignCredential
	err := s.Store.Update(PlatformCodeArts, accountID, &CodeArtsRecord{}, func(v any) (bool, error) {
		rec := v.(*CodeArtsRecord)
		if rec.UserID == "" && rec.RefreshTok == "" {
			return false, fmt.Errorf("码道账号 %s 记录为空", accountID)
		}
		if !rec.expiresWithin(s.window(), s.now()) {
			result = rec.signCredential()
			return false, nil // 别人刚刷过，不写盘
		}
		out, err := s.Client.RefreshToken(ctx, s.Cfg, rec.RefreshTok, rec.Verifier, codearts.DPoPPrivateJWK(rec.DPoPJWK))
		if err != nil {
			return false, fmt.Errorf("刷新码道凭据失败: %w", err)
		}
		if out.RefreshToken != "" {
			rec.RefreshTok = out.RefreshToken // 一次性轮换：必须覆盖旧值
		}
		rec.AccessKeyID = out.Credentials.AccessKeyID
		rec.SecretAccessKey = out.Credentials.SecretAccessKey
		rec.SecurityToken = out.Credentials.SecurityToken
		rec.Expiration = out.Credentials.Expiration
		rec.UpdatedAt = s.now().Format(time.RFC3339)
		result = rec.signCredential()
		return true, nil
	})
	if err != nil {
		return codearts.SignCredential{}, err
	}
	return result, nil
}
