package panelagg

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts"
	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

func newTestHandler(t *testing.T, stsHandler http.HandlerFunc) (*Handler, *creds.Store) {
	t.Helper()
	st, err := creds.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := codearts.NewClient()
	cfg := codearts.DefaultLoginConfig()
	if stsHandler != nil {
		srv := httptest.NewServer(stsHandler)
		t.Cleanup(srv.Close)
		c.SetEndpoints(srv.URL, "", "")
		cfg.STSHost = srv.URL // requestToken 优先用 cfg.STSHost
	}
	return New(Config{APIKey: "k", Store: st, Client: c, Login: cfg}), st
}

func TestPanelRequiresKeyForAPI(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/panel/agg/api/overview", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 key 应 401，实际 %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/panel/agg/api/overview?key=k", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("带 key 应 200，实际 %d %s", rec.Code, rec.Body)
	}
}

func TestPanelPageNeedsNoKey(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panel/agg/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "码道") {
		t.Fatalf("页面应可打开: %d", rec.Code)
	}
}

func TestStartLoginReturnsAuthorizeURL(t *testing.T) {
	h, _ := newTestHandler(t, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/panel/agg/api/codearts/login?key=k", strings.NewReader("{}")))
	if rec.Code != http.StatusOK {
		t.Fatalf("生成链接失败: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Ticket   string `json:"ticket"`
		LoginURL string `json:"login_url"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Ticket == "" || !strings.Contains(got.LoginURL, "/authorize?") {
		t.Fatalf("返回不对: %+v", got)
	}
	for _, want := range []string{"code_challenge=", "ticket_id=" + got.Ticket, "port=18234"} {
		if !strings.Contains(got.LoginURL, want) {
			t.Errorf("授权链接缺 %q: %s", want, got.LoginURL)
		}
	}
}

func TestCompleteLoginSavesRecord(t *testing.T) {
	h, st := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("DPoP"), ".") {
			t.Error("换 token 必须带 DPoP")
		}
		_, _ = w.Write([]byte(`{"user_id":"u-1","user_name":"测试用户","domain_id":"d-1",
			"refresh_token":"RT-1",
			"credentials":{"access_key_id":"AK","secret_access_key":"SK","security_token":"ST",
			"expiration":"2026-12-01T00:00:00Z"}}`))
	})

	// ① 生成链接拿 ticket
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/panel/agg/api/codearts/login?key=k", strings.NewReader("{}")))
	var start struct {
		Ticket string `json:"ticket"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &start)
	if start.Ticket == "" {
		t.Fatal("没有 ticket")
	}

	// ② 用粘贴回来的回调完成
	body, _ := json.Marshal(map[string]string{
		"ticket":       start.Ticket,
		"callback_url": "http://127.0.0.1:18234/oauth/callback?code=CODE-1&state=x",
	})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/panel/agg/api/codearts/complete?key=k", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("完成登录失败: %d %s", rec.Code, rec.Body)
	}

	var rec1 creds.CodeArtsRecord
	if err := st.Load(creds.PlatformCodeArts, "u-1", &rec1); err != nil {
		t.Fatalf("凭据未落盘: %v", err)
	}
	if rec1.RefreshTok != "RT-1" || rec1.SecurityToken != "ST" {
		t.Errorf("落盘内容不对: %+v", rec1)
	}
	if rec1.DPoPJWK == nil || rec1.Verifier == "" {
		t.Error("必须保存 DPoP 私钥与 PKCE verifier（否则刷新必失败）")
	}

	// ③ 同一个 ticket 不能复用
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/panel/agg/api/codearts/complete?key=k", strings.NewReader(string(body))))
	if rec.Code != http.StatusNotFound {
		t.Errorf("ticket 应一次性，第二次应 404，实际 %d", rec.Code)
	}
}

func TestCompleteLoginRejectsBadInput(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	// 未知 ticket
	body, _ := json.Marshal(map[string]string{"ticket": "nope", "callback_url": "http://x/?code=1"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/panel/agg/api/codearts/complete?key=k", strings.NewReader(string(body))))
	if rec.Code != http.StatusNotFound {
		t.Errorf("未知 ticket 应 404，实际 %d", rec.Code)
	}

	// 缺 code
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/panel/agg/api/codearts/login?key=k", strings.NewReader("{}")))
	var start struct {
		Ticket string `json:"ticket"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &start)
	body, _ = json.Marshal(map[string]string{"ticket": start.Ticket, "callback_url": "http://127.0.0.1:18234/oauth/callback"})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/panel/agg/api/codearts/complete?key=k", strings.NewReader(string(body))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("缺 code 应 400，实际 %d", rec.Code)
	}
}

func TestOverviewListsAccounts(t *testing.T) {
	h, st := newTestHandler(t, nil)
	_ = st.Save(creds.PlatformCodeArts, "u-1", creds.CodeArtsRecord{
		UserID: "u-1", UserName: "alice", AccessKeyID: "AK", SecretAccessKey: "SK",
		SecurityToken: "ST", RefreshTok: "RT", DPoPJWK: map[string]string{"d": "d"},
		Expiration: time.Now().Add(time.Hour).Format(time.RFC3339),
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panel/agg/api/overview?key=k", nil))
	var got struct {
		CodeArts []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"codearts"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.CodeArts) != 1 || got.CodeArts[0].State != "ok" {
		t.Fatalf("概览不对: %+v", got)
	}
}

// 确保回调地址里带 fragment/额外参数也能解析出 code
func TestCallbackURLParsing(t *testing.T) {
	u, err := url.Parse("http://127.0.0.1:18234/oauth/callback?code=abc&state=xyz#frag")
	if err != nil || u.Query().Get("code") != "abc" {
		t.Fatalf("解析失败: %v", err)
	}
}
