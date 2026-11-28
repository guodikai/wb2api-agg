// codearts.go 码道凭据来源：文件存储 + 到期前自动刷新（照抄参照实现的刷新语义）。
package creds

import (
	"strings"
	"sort"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts"
	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts/auth"
)

// PlatformCodeArts 码道在凭据目录里的子目录名。
const PlatformCodeArts = "codearts"

// CodeArtsRecord 一个码道账号的持久化凭据（字段与参照实现的 auths 文件一致）。
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
	Expiration      string            `json:"expiration"`
	UpdatedAt       string            `json:"updated_at"`

	// 积分/套餐（后台每 10 分钟刷新，只读接口 statistics/plugin）
	Credits          float64 `json:"credits,omitempty"`
	CreditsTotal     float64 `json:"credits_total,omitempty"`
	CreditsUsed      float64 `json:"credits_used,omitempty"`
	CreditsExpiring  float64 `json:"credits_expiring,omitempty"`
	CreditsPeriod    string  `json:"credits_period,omitempty"`
	CreditsUpdatedAt string             `json:"credits_updated_at,omitempty"`
	CreditsBreakdown map[string]float64 `json:"credits_breakdown,omitempty"`

	// Disabled 人工禁用（面板可切换）：不参与选号，凭据保留。
	Disabled bool `json:"disabled,omitempty"`
}

// Auth 转成参照实现的账号对象（导出版本，供面板等外部包使用）。
func (r *CodeArtsRecord) Auth() *auth.Auth { return r.toAuth() }

// toAuth 转成参照实现的账号对象。
func (r *CodeArtsRecord) toAuth() *auth.Auth {
	return auth.New(r.UserID, r.UserName, r.DomainID, r.SecurityToken,
		r.AccessKeyID, r.SecretAccessKey, r.Expiration, r.RefreshTok, r.Verifier, r.DPoPJWK)
}

func (r *CodeArtsRecord) expiresWithin(d time.Duration, now time.Time) bool {
	if r.SecurityToken == "" || r.AccessKeyID == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, r.Expiration)
	if err != nil {
		if t, err = time.Parse("2006-01-02T15:04:05.000Z", r.Expiration); err != nil {
			return true
		}
	}
	return now.Add(d).After(t)
}

// Refresher 刷新能力（生产是 *codearts.Client；测试可注入假实现）。
type Refresher interface {
	RefreshToken(ctx context.Context, cfg codearts.LoginConfig, refreshToken, codeVerifier string, jwk codearts.DPoPPrivateJWK) (*codearts.TokenResponse, error)
}

// CodeArtsSource 实现 codearts.CredentialSource。
type CodeArtsSource struct {
	Store         *Store
	Client        Refresher
	Cfg           codearts.LoginConfig
	Now           func() time.Time
	RefreshWindow time.Duration
}

// NewCodeArtsSource 构造。
func NewCodeArtsSource(st *Store, c Refresher, cfg codearts.LoginConfig) *CodeArtsSource {
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

// Any 第一个可用账号（目录探测用）。
func (s *CodeArtsSource) Any(ctx context.Context) (string, *auth.Auth, error) {
	ids, err := s.Store.List(PlatformCodeArts)
	if err != nil {
		return "", nil, err
	}
	if len(ids) == 0 {
		return "", nil, fmt.Errorf("没有已登录的码道账号")
	}

	type cand struct {
		id      string
		expiry  time.Time
		credits float64
	}
	cands := make([]cand, 0, len(ids))
	for _, id := range ids {
		var rec CodeArtsRecord
		if err := s.Store.Load(PlatformCodeArts, id, &rec); err != nil {
			continue
		}
		if rec.Disabled {
			continue
		}
		exp := creditsExpiry(rec)
		// 有 24 小时内到期的积分（每日签到那 100 分）→ 视为最急，排最前
		if rec.CreditsExpiring > 0 {
			exp = time.Now()
		}
		cands = append(cands, cand{id: id, expiry: exp, credits: rec.Credits})
	}
	if len(cands) == 0 {
		return "", nil, fmt.Errorf("码道账号均已禁用或不可读")
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if !a.expiry.IsZero() && !b.expiry.IsZero() {
			if !a.expiry.Equal(b.expiry) {
				return a.expiry.Before(b.expiry)
			}
		} else if !a.expiry.IsZero() != !b.expiry.IsZero() {
			return !a.expiry.IsZero()
		}
		return a.credits > b.credits
	})

	var lastErr error
	for _, c := range cands {
		a, err := s.For(ctx, c.id)
		if err == nil {
			return c.id, a, nil
		}
		lastErr = err
	}
	return "", nil, fmt.Errorf("码道账号均不可用: %v", lastErr)
}

// creditsExpiry 从 "2026-11-01 ~ 2026-12-23" 取右半段。
func creditsExpiry(rec CodeArtsRecord) time.Time {
	parts := strings.SplitN(rec.CreditsPeriod, "~", 2)
	if len(parts) != 2 {
		return time.Time{}
	}
	v := strings.TrimSpace(parts[1])
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// For 指定账号；临近到期先刷新。
func (s *CodeArtsSource) For(ctx context.Context, accountID string) (*auth.Auth, error) {
	var rec CodeArtsRecord
	if err := s.Store.Load(PlatformCodeArts, accountID, &rec); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("码道账号 %s 不存在", accountID)
		}
		return nil, err
	}
	if !rec.expiresWithin(s.window(), s.now()) {
		return rec.toAuth(), nil
	}
	if rec.RefreshTok == "" || rec.DPoPJWK == nil {
		return nil, fmt.Errorf("码道账号 %s 缺少 refresh_token/DPoP 私钥，需要重新登录", accountID)
	}
	return s.refresh(ctx, accountID)
}

// refresh 锁内「重读 → 判断 → 刷新 → 落盘」，避免并发重复消费一次性 RT。
func (s *CodeArtsSource) refresh(ctx context.Context, accountID string) (*auth.Auth, error) {
	var result *auth.Auth
	err := s.Store.Update(PlatformCodeArts, accountID, &CodeArtsRecord{}, func(v any) (bool, error) {
		rec := v.(*CodeArtsRecord)
		if rec.RefreshTok == "" {
			return false, fmt.Errorf("码道账号 %s 记录为空", accountID)
		}
		if !rec.expiresWithin(s.window(), s.now()) {
			result = rec.toAuth()
			return false, nil
		}
		out, err := s.Client.RefreshToken(ctx, s.Cfg, rec.RefreshTok, rec.Verifier,
			codearts.DPoPPrivateJWK(rec.DPoPJWK))
		if err != nil {
			return false, fmt.Errorf("刷新码道凭据失败: %w", err)
		}
		if out.RefreshToken != "" {
			rec.RefreshTok = out.RefreshToken
		}
		rec.AccessKeyID = out.Credentials.AccessKeyID
		rec.SecretAccessKey = out.Credentials.SecretAccessKey
		rec.SecurityToken = out.Credentials.SecurityToken
		rec.Expiration = out.Credentials.Expiration
		rec.UpdatedAt = s.now().Format(time.RFC3339)
		result = rec.toAuth()
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
