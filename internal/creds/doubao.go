// doubao.go 豆包凭据：只读来源 + cookie 解析。
//
// 与码道不同，豆包这里**不做刷新**：cookie 的保活由签到引擎（L0NE DoubaoWork-Daily）负责，
// 网关只读。cookie 失效时返回明确错误，让面板标红并提示重新导入。
package creds

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel/doubao"
)

// PlatformDoubao 豆包在凭据目录里的子目录名。
const PlatformDoubao = "doubao"

// DoubaoRecord 一个豆包账号的持久化凭据（字段与面板导入一致）。
type DoubaoRecord struct {
	ID        string            `json:"id"`
	Name      string            `json:"name,omitempty"`
	Cookies   map[string]string `json:"cookies"`
	DeviceID  string            `json:"device_id"`
	WebID     string            `json:"web_id"`
	FP        string            `json:"fp"`
	MSToken   string            `json:"ms_token"`
	AppID     string            `json:"app_id,omitempty"`
	PCVersion string            `json:"pc_version,omitempty"`
	UserAgent string            `json:"user_agent,omitempty"`
	UpdatedAt string            `json:"updated_at"`
}

// Account 转成协议层的账号对象。
func (r *DoubaoRecord) Account() *doubao.Account {
	return &doubao.Account{
		ID: r.ID, Cookies: r.Cookies,
		DeviceID: r.DeviceID, WebID: r.WebID, FP: r.FP, MSToken: r.MSToken,
		AppID: r.AppID, PCVersion: r.PCVersion, UserAgent: r.UserAgent,
	}
}

// RequiredCookies 豆包工作对话的最小 cookie 集（与参照实现一致）。
var RequiredCookies = []string{
	"sessionid", "sessionid_ss", "sid_tt", "sid_guard",
	"uid_tt", "uid_tt_ss", "ttwid", "passport_csrf_token",
}

// ParseCookieString 解析浏览器粘出来的 "k=v; k=v" 形式。
func ParseCookieString(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.IndexByte(part, '=')
		if i <= 0 {
			continue
		}
		k := strings.TrimSpace(part[:i])
		v := strings.TrimSpace(part[i+1:])
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// MissingCookies 返回缺失的必需 cookie（空 = 齐全）。
func MissingCookies(cookies map[string]string) []string {
	var miss []string
	for _, k := range RequiredCookies {
		if strings.TrimSpace(cookies[k]) == "" {
			miss = append(miss, k)
		}
	}
	return miss
}

// DoubaoSource 实现 doubao.CredentialSource（只读）。
type DoubaoSource struct {
	Store *Store
}

// NewDoubaoSource 构造。
func NewDoubaoSource(st *Store) *DoubaoSource { return &DoubaoSource{Store: st} }

// Any 第一个账号。
func (s *DoubaoSource) Any(ctx context.Context) (*doubao.Account, error) {
	ids, err := s.Store.List(PlatformDoubao)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("没有已导入的豆包账号")
	}
	return s.For(ctx, ids[0])
}

// For 指定账号。
func (s *DoubaoSource) For(ctx context.Context, id string) (*doubao.Account, error) {
	var rec DoubaoRecord
	if err := s.Store.Load(PlatformDoubao, id, &rec); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("豆包账号 %s 不存在", id)
		}
		return nil, err
	}
	if miss := MissingCookies(rec.Cookies); len(miss) > 0 {
		return nil, fmt.Errorf("豆包账号 %s 缺少 cookie: %s（请重新导入）", id, strings.Join(miss, ", "))
	}
	if rec.DeviceID == "" || rec.WebID == "" {
		return nil, fmt.Errorf("豆包账号 %s 缺少 device_id/web_id（设备参数必须成套，请用面板的导入脚本重新取一次）", id)
	}
	return rec.Account(), nil
}
