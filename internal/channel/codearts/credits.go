// credits.go 码道积分/套餐查询（只读）。
//
// 端点：GET https://snap-access.cn-north-4.myhuaweicloud.com/snap-manager/v1/statistics/plugin
// 要求：AK/SK 签名（SDK-HMAC-SHA256）+ Agent-Type: PromptCenter + X-Language: zh-cn
// 来源：iJetLi/dsh-providers-hub 的 codearts-credits.ts（MIT），2026-11-27 实测 200。
//
// 响应是"裸对象"（没有 code 字段），关键数据在 metrics[] 里，按 name 区分口径：
//
//	usageTotalPackageCredit  总积分包：package_credit_amount / used / remain / expiring
//	usageBasicPackageCredit  基础积分包
//	usageTokenChatMessages   token 用量
package codearts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// EpStatisticsPlugin 积分/套餐统计（snap-manager 层，与模型目录不同前缀）。
const EpStatisticsPlugin = "/snap-manager/v1/statistics/plugin"

// CreditMetric metrics[] 的一条（字段按需取，未出现的保持零值）。
type CreditMetric struct {
	Name                      string  `json:"name"`
	Value                     float64 `json:"value"`
	Show                      bool    `json:"show"`
	PackageCreditAmount       float64 `json:"package_credit_amount"`
	PackageCreditUsed         float64 `json:"package_credit_used"`
	PackageCreditRemain       float64 `json:"package_credit_remain"`
	PackageCreditExpiring     float64 `json:"package_credit_expiring_amount"`
	PackageCreditAvailable    float64 `json:"package_credit_available"`
	PackageCreditUserPool     float64 `json:"package_credit_user_pool_amount"`
	TotalCreditPool           float64 `json:"total_credit_pool"`
	UsedCreditPool            float64 `json:"used_credit_pool"`
	PackageTokenAmount        float64 `json:"package_token_amount"`
	UsageTokenNum             float64 `json:"usage_token_num"`
}

// PackageInfo 一次 statistics/plugin 的解析结果。
type PackageInfo struct {
	Metrics    []CreditMetric  `json:"metrics"`
	Package    map[string]any  `json:"package"`
	StartDate  string          `json:"start_date"`
	EndDate    string          `json:"end_date"`
	IsWhite    bool            `json:"is_white_list"`
}

// Metric 按 name 取一条指标。
func (p *PackageInfo) Metric(name string) (CreditMetric, bool) {
	for _, m := range p.Metrics {
		if m.Name == name {
			return m, true
		}
	}
	return CreditMetric{}, false
}

// Summary 汇总给人看的四个数：总额 / 已用 / 剩余 / 即将过期。
func (p *PackageInfo) Summary() (total, used, remain, expiring float64, period string) {
	if m, ok := p.Metric("usageTotalPackageCredit"); ok {
		total = m.PackageCreditAmount
		if total == 0 {
			total = m.TotalCreditPool
		}
		used = m.PackageCreditUsed
		if used == 0 {
			used = m.UsedCreditPool
		}
		remain = m.PackageCreditRemain
		if remain == 0 {
			remain = m.PackageCreditAvailable
		}
		expiring = m.PackageCreditExpiring
	}
	period = p.StartDate
	if p.EndDate != "" {
		period = p.StartDate + " ~ " + p.EndDate
	}
	return
}

// Breakdown 积分构成（面板展示用）：口径名 → 剩余值。
// 只取有意义的项，值为 0 的也保留（说明该口径存在但已用尽）。
func (p *PackageInfo) Breakdown() map[string]float64 {
	out := map[string]float64{}
	for _, m := range p.Metrics {
		if !m.Show {
			continue
		}
		switch m.Name {
		case "usageTotalPackageCredit":
			out["总积分包"] = m.PackageCreditRemain
			out["用户池"] = m.PackageCreditUserPool
			out["即将过期"] = m.PackageCreditExpiring
		case "usageBasicPackageCredit":
			out["基础积分包"] = m.PackageCreditRemain
		case "usageTokenChatMessages":
			out["对话 token 用量"] = m.UsageTokenNum
		case "usageDataChatMessages":
			out["对话次数"] = m.Value
		}
	}
	return out
}

// FetchPackageInfo 查一次积分/套餐（只读，不消耗额度）。
func (c *Client) FetchPackageInfo(ctx context.Context, cred SignCredential) (*PackageInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SnapEngineApiHost+EpStatisticsPlugin, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Agent-Type", "PromptCenter")
	req.Header.Set("X-Language", "zh-cn")
	if cred.SecurityToken != "" {
		req.Header.Set("X-Security-Token", cred.SecurityToken)
	}
	// 签名必须覆盖上面所有头（含 Agent-Type / X-Language，与参照实现一致）。
	signRequest(req, []byte{}, cred)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 400 {
		return nil, &ApiError{Status: resp.StatusCode, Message: truncateStr(string(raw), 200), Path: EpStatisticsPlugin}
	}
	var out PackageInfo
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreditsRefreshInterval 积分刷新间隔（只读接口，10 分钟足够，别打太勤）。
const CreditsRefreshInterval = 10 * time.Minute
