// codearts_credits.go 码道积分的后台刷新与面板数据源。
//
// 设计要点：
//   - 只读接口（statistics/plugin），10 分钟一轮，别打太勤
//   - 刷新结果写进凭据记录（Credits/CreditsTotal/…），账号池与面板都从这里读
//   - 任何失败只记日志，不影响 chat 通道
package agg

import (
	"context"
	"log"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts"
	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

// StartCodeArtsCreditsRefresh 起一个后台循环刷新码道积分。
// 返回的 cancel 用于退出；重复调用会起多个循环，调用方自己保证只调一次。
func StartCodeArtsCreditsRefresh(ctx context.Context, st *creds.Store, interval time.Duration) context.CancelFunc {
	if st == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = codearts.CreditsRefreshInterval
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		// 启动先跑一轮（面板马上就有数），之后按间隔。
		refreshCodeArtsCredits(ctx, st)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				refreshCodeArtsCredits(ctx, st)
			}
		}
	}()
	return cancel
}

// refreshCodeArtsCredits 对每个码道账号查一次积分并落盘。
func refreshCodeArtsCredits(ctx context.Context, st *creds.Store) {
	ids, err := st.List(creds.PlatformCodeArts)
	if err != nil {
		return
	}
	if len(ids) == 0 {
		return
	}
	client := codearts.New(30 * time.Second)
	for _, id := range ids {
		var rec creds.CodeArtsRecord
		if err := st.Load(creds.PlatformCodeArts, id, &rec); err != nil {
			continue
		}
		if rec.AccessKeyID == "" || rec.SecretAccessKey == "" {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		info, err := client.FetchPackageInfo(cctx, codearts.SignCredential{
			AccessKeyID:     rec.AccessKeyID,
			SecretAccessKey: rec.SecretAccessKey,
			SecurityToken:   rec.SecurityToken,
		})
		cancel()
		if err != nil {
			log.Printf("[agg] 码道积分刷新失败 %s: %v", id, err)
			continue
		}
		total, used, remain, expiring, period := info.Summary()
		rec.Credits = remain
		rec.CreditsTotal = total
		rec.CreditsUsed = used
		rec.CreditsExpiring = expiring
		rec.CreditsPeriod = period
		rec.CreditsUpdatedAt = time.Now().Format(time.RFC3339)
		if err := st.Save(creds.PlatformCodeArts, id, &rec); err != nil {
			log.Printf("[agg] 码道积分落盘失败 %s: %v", id, err)
		}
	}
}
