package creds

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts"
)

type fakeRefresher struct {
	calls int32
	delay time.Duration
	out   *codearts.TokenResponse
	err   error
}

func (f *fakeRefresher) RefreshToken(context.Context, codearts.LoginConfig, string, string, codearts.DPoPPrivateJWK) (*codearts.TokenResponse, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func seed(t *testing.T, st *Store, id string, rec CodeArtsRecord) {
	t.Helper()
	if err := st.Save(PlatformCodeArts, id, rec); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRoundTripAndList(t *testing.T) {
	st := newTestStore(t)
	seed(t, st, "u2", CodeArtsRecord{UserID: "u2"})
	seed(t, st, "u1", CodeArtsRecord{UserID: "u1"})

	ids, err := st.List(PlatformCodeArts)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "u1" || ids[1] != "u2" {
		t.Fatalf("List = %v", ids)
	}
	var rec CodeArtsRecord
	if err := st.Load(PlatformCodeArts, "u1", &rec); err != nil || rec.UserID != "u1" {
		t.Fatalf("Load 失败: %v %+v", err, rec)
	}
	if err := st.Load(PlatformCodeArts, "nope", &rec); !os.IsNotExist(err) {
		t.Errorf("不存在的账号应返回 ErrNotExist，实际 %v", err)
	}
}

func TestSourceFreshCredentialNeedsNoRefresh(t *testing.T) {
	st := newTestStore(t)
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	seed(t, st, "u1", CodeArtsRecord{
		UserID: "u1", AccessKeyID: "AK", SecretAccessKey: "SK", SecurityToken: "ST",
		Expiration: now.Add(2 * time.Hour).Format(time.RFC3339),
	})
	fr := &fakeRefresher{}
	src := NewCodeArtsSource(st, fr, codearts.DefaultLoginConfig())
	src.Now = func() time.Time { return now }

	cred, err := src.For(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccessKeyID != "AK" || cred.SecurityToken != "ST" {
		t.Errorf("凭据不对: %+v", cred)
	}
	if fr.calls != 0 {
		t.Errorf("未临近到期不该刷新，实际刷新 %d 次", fr.calls)
	}
}

func TestSourceRefreshesWhenExpiringAndRotatesRT(t *testing.T) {
	st := newTestStore(t)
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	seed(t, st, "u1", CodeArtsRecord{
		UserID:     "u1",
		RefreshTok: "OLD_RT",
		DPoPJWK:    map[string]string{"kty": "EC", "crv": "P-256", "x": "x", "y": "y", "d": "d"},
		Expiration: now.Add(2 * time.Minute).Format(time.RFC3339), // 在 10 分钟窗口内
	})
	fr := &fakeRefresher{out: &codearts.TokenResponse{
		RefreshToken: "NEW_RT",
		Credentials:  codearts.Credentials{AccessKeyID: "AK2", SecretAccessKey: "SK2", SecurityToken: "ST2", Expiration: now.Add(1 * time.Hour).Format(time.RFC3339)},
	}}
	src := NewCodeArtsSource(st, fr, codearts.DefaultLoginConfig())
	src.Now = func() time.Time { return now }

	cred, err := src.For(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if cred.AccessKeyID != "AK2" {
		t.Errorf("应返回刷新后的凭据: %+v", cred)
	}
	var rec CodeArtsRecord
	_ = st.Load(PlatformCodeArts, "u1", &rec)
	if rec.RefreshTok != "NEW_RT" {
		t.Errorf("refresh_token 必须被轮换覆盖，实际 %q", rec.RefreshTok)
	}
	if rec.SecurityToken != "ST2" {
		t.Errorf("STS 未落盘: %+v", rec)
	}
}

// 并发进来多个请求：只能刷一次（RT 一次性轮换，刷两次就把号刷废）。
func TestSourceConcurrentRefreshOnlyOnce(t *testing.T) {
	for _, delay := range []time.Duration{0, 20 * time.Millisecond} {
		st := newTestStore(t)
		now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
		seed(t, st, "u1", CodeArtsRecord{
			UserID: "u1", RefreshTok: "OLD_RT",
			DPoPJWK:    map[string]string{"kty": "EC", "crv": "P-256", "x": "x", "y": "y", "d": "d"},
			Expiration: now.Add(-time.Hour).Format(time.RFC3339),
		})
		fr := &fakeRefresher{delay: delay, out: &codearts.TokenResponse{
			RefreshToken: "NEW_RT",
			Credentials:  codearts.Credentials{AccessKeyID: "AK", SecretAccessKey: "SK", SecurityToken: "ST", Expiration: now.Add(time.Hour).Format(time.RFC3339)},
		}}
		src := NewCodeArtsSource(st, fr, codearts.DefaultLoginConfig())
		src.Now = func() time.Time { return now }

		var wg sync.WaitGroup
		errs := make([]error, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, errs[i] = src.For(context.Background(), "u1")
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("goroutine %d 失败: %v", i, err)
			}
		}
		if got := atomic.LoadInt32(&fr.calls); got != 1 {
			t.Errorf("delay=%v 应只刷新 1 次，实际 %d 次", delay, got)
		}
	}
}

func TestSourceMissingRecordAndMissingJWK(t *testing.T) {
	st := newTestStore(t)
	src := NewCodeArtsSource(st, &fakeRefresher{}, codearts.DefaultLoginConfig())

	if _, err := src.For(context.Background(), "ghost"); err == nil {
		t.Error("不存在的账号应报错")
	}
	if _, _, err := src.Any(context.Background()); err == nil {
		t.Error("空目录 Any 应报错")
	}

	seed(t, st, "u1", CodeArtsRecord{UserID: "u1", AccessKeyID: "AK", SecurityToken: "ST", Expiration: "2020-01-01T00:00:00Z"})
	if _, err := src.For(context.Background(), "u1"); err == nil {
		t.Error("缺 refresh_token/DPoP 时应提示重新登录")
	}
}

func TestSourceRefreshErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	seed(t, st, "u1", CodeArtsRecord{
		UserID: "u1", RefreshTok: "RT", DPoPJWK: map[string]string{"d": "d"},
		Expiration: "2020-01-01T00:00:00Z",
	})
	fr := &fakeRefresher{err: errors.New("invalid_grant")}
	src := NewCodeArtsSource(st, fr, codearts.DefaultLoginConfig())
	if _, err := src.For(context.Background(), "u1"); err == nil {
		t.Error("刷新失败应向上抛错")
	}
}

func TestSourceAnyPicksFirstUsable(t *testing.T) {
	st := newTestStore(t)
	now := time.Now()
	seed(t, st, "bad", CodeArtsRecord{UserID: "bad", Expiration: "2020-01-01T00:00:00Z"}) // 无 RT/JWK，不可用
	seed(t, st, "good", CodeArtsRecord{UserID: "good", AccessKeyID: "AK", SecretAccessKey: "SK", SecurityToken: "ST", Expiration: now.Add(time.Hour).Format(time.RFC3339)})
	src := NewCodeArtsSource(st, &fakeRefresher{}, codearts.DefaultLoginConfig())

	id, cred, err := src.Any(context.Background())
	if err != nil {
		t.Fatalf("Any 失败: %v", err)
	}
	if id != "good" || cred.AccessKeyID != "AK" {
		t.Errorf("Any 应跳过不可用账号，实际 id=%s cred=%+v", id, cred)
	}
}
