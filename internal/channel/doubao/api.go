// Package doubao 豆包（豆包工作 / 桌面客户端）消费通道。
//
// 协议依据（两路独立证据交叉验证，2026-11-26）：
//  1. APK `dev.doubao2api` 1.0.6 注入 WebView 的 JS：设备身份四件套来自真实网页客户端
//     — device_id ← localStorage['samantha_web_web_id'].web_id
//     — web_id     ← localStorage['__tea_cache_tokens_497858'].web_id
//     — fp         ← cookie s_v_web_id
//     — ms_token   ← cookie msToken
//     请求只带 Content-Type + agw-js-conv: str + x-tt-passport-csrf-token。
//  2. miaocell/magpie-doubao-work-auth（MIT，11-25 仍在更新）：设备参数必须成套自洽，
//     风控码 710022004 需要人工过验证，710022002 是过频。
//
// 因此本包不做任何签名（豆包 chat 不校验 a_bogus/msToken 签名），只保证"设备参数成套"。
package doubao

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// 端点常量。
const (
	Host = "www.doubao.com"
	Base = "https://" + Host

	// PathChat 普通对话；PathSamantha 是"工作/Agent"形态的对话端点。
	PathChat     = "/chat/completion"
	PathSamantha = "/samantha/chat/completion"

	// 只读接口（不消耗额度，用作接入探针）。
	PathQuotaSummary = "/alice/commerce/sale/subscription/quota/summary/"
	PathOverview     = "/alice/commerce/sale/subscription/overview/"
)

// 默认值（与网页/桌面客户端一致；账号可覆盖）。
const (
	DefaultAid         = "582478"
	DefaultPCVersion   = "3.41.2"
	DefaultRuntimeVer  = "3.41.2"
	DefaultVersionCode = "20800"
	DefaultUA          = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// Account 一个豆包账号的全部请求要素。
type Account struct {
	ID string
	// Cookies 至少包含 sessionid / sessionid_ss / sid_tt / sid_guard / uid_tt / uid_tt_ss
	// / ttwid / passport_csrf_token（miaocell 与旧实现一致的最小集）。
	Cookies map[string]string
	// 设备身份四件套（必须成套自洽，缺一容易被风控）
	DeviceID string
	WebID    string
	FP       string
	MSToken  string

	AppID        string
	PCVersion    string
	RuntimeVer   string
	UserAgent    string
}

func (a *Account) aid() string {
	if a.AppID != "" {
		return a.AppID
	}
	return DefaultAid
}

func (a *Account) pcVersion() string {
	if a.PCVersion != "" {
		return a.PCVersion
	}
	return DefaultPCVersion
}

func (a *Account) runtimeVersion() string {
	if a.RuntimeVer != "" {
		return a.RuntimeVer
	}
	return DefaultRuntimeVer
}

func (a *Account) ua() string {
	if a.UserAgent != "" {
		return a.UserAgent
	}
	return DefaultUA
}

func (a *Account) csrf() string {
	for _, k := range []string{"passport_csrf_token", "passport_csrf_token_default"} {
		if v := a.Cookies[k]; v != "" {
			return v
		}
	}
	return ""
}

// cookieHeader 拼 Cookie 头（键名排序，输出稳定）。
func (a *Account) cookieHeader() string {
	keys := make([]string, 0, len(a.Cookies))
	for k := range a.Cookies {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+a.Cookies[k])
	}
	return strings.Join(parts, "; ")
}

// query 设备面参数（成套自洽；顺序固定便于比对抓包）。
func (a *Account) query() string {
	ordered := [][2]string{
		{"aid", a.aid()},
		{"real_aid", a.aid()},
		{"device_id", a.DeviceID},
		{"tea_uuid", a.DeviceID},
		{"web_id", a.WebID},
		{"fp", a.FP},
		{"device_platform", "web"},
		{"doubao_device_platform", "desktop"},
		{"language", "zh"},
		{"region", "CN"},
		{"sys_region", "CN"},
		{"pkg_type", "release_version"},
		{"version_code", DefaultVersionCode},
		{"pc_version", a.pcVersion()},
		{"doubao_pc_version", a.pcVersion()},
		{"runtime", "web"},
		{"runtime_version", a.runtimeVersion()},
		{"samantha_web", "1"},
		{"use-olympus-account", "1"},
		{"web_platform", "desktop"},
	}
	vals := url.Values{}
	for _, kv := range ordered {
		if kv[1] != "" {
			vals.Set(kv[0], kv[1])
		}
	}
	if a.MSToken != "" {
		vals.Set("msToken", a.MSToken)
	}
	// url.Values.Encode 会按 key 排序，顺序与抓包不完全一致；豆包不校验顺序，但它
	// 会校验"参数成套"，所以这里保留全部键即可。
	return vals.Encode()
}

// Client 豆包 HTTP 客户端。
type Client struct {
	http      *http.Client
	base      string // 可被测试覆盖
	streamHTTP *http.Client
}

// New 构造。
func New(base string) *Client {
	if base == "" {
		base = Base
	}
	tr := &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		http:       &http.Client{Timeout: 60 * time.Second, Transport: tr},
		streamHTTP: &http.Client{Transport: tr},
		base:       base,
	}
}

// UpstreamError 上游错误（含风控码）。
type UpstreamError struct {
	Status int
	Code   string // 业务码（如 710022004）
	Body   string
	Path   string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("doubao upstream %d code=%s (%s): %s", e.Status, e.Code, e.Path, e.Body)
}

// IsVerification 报告是否属于"需要人工过验证"的风控。
func (e *UpstreamError) IsVerification() bool {
	switch e.Code {
	case "710022004", "710022002", "403", "412", "712010702":
		return true
	}
	return false
}

// do 发一次请求，返回响应体（调用方关闭）。
func (c *Client) do(ctx context.Context, method, path, query string, body io.Reader, acct *Account, stream bool) (*http.Response, error) {
	u := c.base + path
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("agw-js-conv", "str")
	req.Header.Set("Origin", Base)
	req.Header.Set("Referer", Base+"/chat/")
	req.Header.Set("User-Agent", acct.ua())
	if ck := acct.cookieHeader(); ck != "" {
		req.Header.Set("Cookie", ck)
	}
	if csrf := acct.csrf(); csrf != "" {
		req.Header.Set("x-tt-passport-csrf-token", csrf)
	}

	hc := c.http
	if stream {
		hc = c.streamHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		return nil, &UpstreamError{
			Status: resp.StatusCode, Code: businessCode(raw),
			Body: truncate(string(raw), 300), Path: path,
		}
	}
	return resp, nil
}

// QuotaSummary 只读探针：额度汇总（不消耗额度）。
func (c *Client) QuotaSummary(ctx context.Context, acct *Account) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, PathQuotaSummary, acct.query(), nil, acct, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// businessCode 尽力从响应体里抽业务码（JSON 字段或裸数字）。
func businessCode(raw []byte) string {
	s := string(raw)
	for _, k := range []string{`"code":`, `"error_code":`} {
		if i := strings.Index(s, k); i >= 0 {
			rest := s[i+len(k):]
			rest = strings.TrimLeft(rest, " \"")
			end := 0
			for end < len(rest) && (rest[end] >= '0' && rest[end] <= '9' || rest[end] == '"') {
				end++
			}
			if code := strings.Trim(rest[:end], `"`); code != "" {
				return code
			}
		}
	}
	if strings.Contains(s, "captcha") || strings.Contains(s, "验证") {
		return "710022004"
	}
	return ""
}
