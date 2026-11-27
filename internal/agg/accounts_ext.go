// accounts_ext.go 处理主面板对"扩展平台账号"的行内操作。
//
// 账号池里我们注入了 codearts:xxx 这样的行；主面板按钮会按 uid 打
// /panel/api/accounts/{uid}/{balance|checkin|tasks|revive|disable|...}。
// 上游不认识这个 uid，这里接管：
//   - balance：真的刷新码道积分后返回，按钮可用
//   - 其它：返回明确的 400 + 说明（不把"未接入"伪装成成功）
package agg

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts"
	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

// handleExtAccountAction 处理扩展平台账号的操作请求；返回 true 表示已处理。
func (m *Mux) handleExtAccountAction(w http.ResponseWriter, r *http.Request, st *creds.Store) bool {
	const prefix = "/panel/api/accounts/"
	if !strings.HasPrefix(r.URL.Path, prefix) || st == nil {
		return false
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return false
	}
	uid, action := rest[:slash], rest[slash+1:]
	if !strings.HasPrefix(uid, uidPrefixCodeArts) {
		return false
	}
	acctID := strings.TrimPrefix(uid, uidPrefixCodeArts)

	switch action {
	case "balance":
		var rec creds.CodeArtsRecord
		if err := st.Load(creds.PlatformCodeArts, acctID, &rec); err != nil {
			writeJSONError(w, http.StatusNotFound, "account_not_found", "码道账号 %s 不存在", acctID)
			return true
		}
		client := codearts.New(25 * time.Second)
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		info, err := client.FetchPackageInfo(ctx, codearts.SignCredential{
			AccessKeyID:     rec.AccessKeyID,
			SecretAccessKey: rec.SecretAccessKey,
			SecurityToken:   rec.SecurityToken,
		})
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "upstream_error", "刷新码道积分失败：%s", err.Error())
			return true
		}
		total, used, remain, expiring, period := info.Summary()
		rec.Credits, rec.CreditsTotal, rec.CreditsUsed = remain, total, used
		rec.CreditsExpiring, rec.CreditsPeriod = expiring, period
		rec.CreditsBreakdown = info.Breakdown()
		rec.CreditsUpdatedAt = time.Now().Format(time.RFC3339)
		_ = st.Save(creds.PlatformCodeArts, acctID, &rec)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "credits": remain, "credits_total": total,
			"credits_used": used, "credits_expiring": expiring,
			"credits_period": period, "credits_breakdown": rec.CreditsBreakdown,
		})
		return true
	case "checkin", "tasks", "revive", "disable", "delete", "probe_ban":
		writeJSONError(w, http.StatusBadRequest, "not_supported_for_platform",
			"码道账号不支持该操作（%s）：签到/任务由 L0NE 脚本负责，网关只做消费与积分查询", action)
		return true
	}
	return false
}
