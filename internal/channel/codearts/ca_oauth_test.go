package codearts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *LoginConfig) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewClient()
	cfg := DefaultLoginConfig()
	cfg.STSHost = srv.URL
	return c, &cfg
}

func TestRefreshTokenSendsFormAndDPoP(t *testing.T) {
	var got url.Values
	var dpop string
	c, cfg := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
			t.Errorf("content-type = %q", ct)
		}
		dpop = r.Header.Get("DPoP")
		_ = r.ParseForm()
		got = r.PostForm
		_, _ = w.Write([]byte(`{"user_id":"u1","refresh_token":"NEW_RT",
			"credentials":{"access_key_id":"AK","secret_access_key":"SK","security_token":"ST","expiration":"2026-12-01T00:00:00Z"}}`))
	})

	jwk, err := NewDPoPPrivateJWK()
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.RefreshToken(context.Background(), *cfg, "OLD_RT", "VERIFIER", jwk)
	if err != nil {
		t.Fatalf("刷新失败: %v", err)
	}
	if out.RefreshToken != "NEW_RT" || out.Credentials.AccessKeyID != "AK" {
		t.Errorf("解析结果不对: %+v", out)
	}
	if got.Get("grant_type") != "refresh_token" || got.Get("refresh_token") != "OLD_RT" ||
		got.Get("client_id") != ClientID || got.Get("code_verifier") != "VERIFIER" {
		t.Errorf("表单字段不对: %v", got)
	}
	if dpop == "" || strings.Count(dpop, ".") != 2 {
		t.Errorf("DPoP 头缺失或格式不对: %q", dpop)
	}
}

func TestRefreshTerminalErrorIsClassified(t *testing.T) {
	cases := []string{
		`{"error":"invalid_grant","error_code":"","error_msg":"refresh token expired"}`,
		`{"error":"invalid_request","error_code":"InvalidDPoPHeader","error_msg":"dpop mismatch"}`,
		`{"error":"invalid_request","error_code":"","error_msg":"refresh token has been used"}`,
	}
	for _, body := range cases {
		body := body
		c, cfg := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(body))
		})
		_, err := c.RefreshToken(context.Background(), *cfg, "RT", "V", nil)
		var terminal *RefreshTokenExpiredError
		if !errors.As(err, &terminal) {
			t.Errorf("应识别为终态失效，实际 %v（body=%s）", err, body)
		}
	}
}

func TestRefreshServerErrorIsNotTerminal(t *testing.T) {
	c, cfg := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`upstream boom`))
	})
	_, err := c.RefreshToken(context.Background(), *cfg, "RT", "V", nil)
	if err == nil {
		t.Fatal("应报错")
	}
	var terminal *RefreshTokenExpiredError
	if errors.As(err, &terminal) {
		t.Error("5xx 不该被判成凭据终态失效（否则会误删可用账号）")
	}
}

func TestRefreshMissingCredentialsIsError(t *testing.T) {
	c, cfg := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"user_id":"u1","refresh_token":"RT"}`)) // 没有 credentials
	})
	if _, err := c.RefreshToken(context.Background(), *cfg, "RT", "V", nil); err == nil {
		t.Error("缺 STS 凭据必须报错")
	}
}

func TestLegacyCredentialIsNormalized(t *testing.T) {
	c, cfg := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"user_id":"u1","credential":{"access":"AK2","secret":"SK2","securitytoken":"ST2","expires_at":"x"}}`))
	})
	out, err := c.RefreshToken(context.Background(), *cfg, "RT", "V", nil)
	if err != nil {
		t.Fatalf("legacy 形态应被接受: %v", err)
	}
	if out.Credentials.AccessKeyID != "AK2" || out.Credentials.SecurityToken != "ST2" {
		t.Errorf("legacy 折叠失败: %+v", out.Credentials)
	}
}

func TestExchangeCodeRedirectURI(t *testing.T) {
	var got url.Values
	c, cfg := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.PostForm
		_, _ = w.Write([]byte(`{"credentials":{"access_key_id":"AK","secret_access_key":"SK","security_token":"ST"}}`))
	})
	if _, err := c.ExchangeCode(context.Background(), *cfg, "CODE", "VER", 18234, nil); err != nil {
		t.Fatalf("换码失败: %v", err)
	}
	if got.Get("grant_type") != "authorization_code" || got.Get("code") != "CODE" {
		t.Errorf("表单不对: %v", got)
	}
	if want := "http://127.0.0.1:18234/oauth/callback"; got.Get("redirect_uri") != want {
		t.Errorf("redirect_uri = %q, want %q", got.Get("redirect_uri"), want)
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	c := NewClient()
	u := c.BuildAuthorizeURL(DefaultLoginConfig(), "ticket123", "challenge456", 18234)
	for _, want := range []string{
		"/authorize?", "uri_scheme=codearts-agent", "client_id=codearts-agent",
		"code_challenge=challenge456", "code_challenge_method=SHA-256",
		"ticket_id=ticket123", "port=18234", "plugin-name=snap_AIIDE",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("授权 URL 缺少 %q: %s", want, u)
		}
	}
}

func TestNewPKCE(t *testing.T) {
	p1, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Verifier) != 43 || p1.Method != "SHA-256" {
		t.Errorf("verifier 长度/方法不对: %d %s", len(p1.Verifier), p1.Method)
	}
	if p1.Challenge == p1.Verifier {
		t.Error("challenge 不应等于 verifier")
	}
	p2, _ := NewPKCE()
	if p1.Verifier == p2.Verifier {
		t.Error("两次生成不应相同")
	}
}

// 确保 json 字段名与上游一致（改名会让解析静默失败）。
func TestTokenResponseJSONTags(t *testing.T) {
	var tr TokenResponse
	raw := `{"credentials":{"access_key_id":"a","secret_access_key":"b","security_token":"c"}}`
	if err := json.Unmarshal([]byte(raw), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.Credentials.SecretAccessKey != "b" {
		t.Errorf("secret_access_key 未映射: %+v", tr.Credentials)
	}
}
