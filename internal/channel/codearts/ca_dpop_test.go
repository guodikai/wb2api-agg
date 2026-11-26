package codearts

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

func mustKeyPair(t *testing.T) *dpopKeyPair {
	t.Helper()
	jwk, err := NewDPoPPrivateJWK()
	if err != nil {
		t.Fatalf("生成 DPoP 私钥失败: %v", err)
	}
	kp, err := dpopKeyPairFromJWK(jwk)
	if err != nil {
		t.Fatalf("还原 DPoP 私钥失败: %v", err)
	}
	return kp
}

// 结构断言：三段式 JWT、header 声明、payload 的 htm/htu/jti/iat，且签名能用公钥验证。
func TestDPoPProofVerifies(t *testing.T) {
	kp := mustKeyPair(t)
	htu := STSHost + EpOAuthTokens
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)

	proof, err := signDPoPProofAt(kp, htu, now)
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT 应为三段，实际 %d", len(parts))
	}

	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("header 解码失败: %v", err)
	}
	var header struct {
		Alg string            `json:"alg"`
		Typ string            `json:"typ"`
		JWK map[string]string `json:"jwk"`
	}
	if err := json.Unmarshal(hb, &header); err != nil {
		t.Fatalf("header 解析失败: %v", err)
	}
	if header.Alg != "ES256" || header.Typ != "dpop+jwt" {
		t.Errorf("header = %+v", header)
	}
	for _, f := range []string{"kty", "crv", "x", "y"} {
		if header.JWK[f] == "" {
			t.Errorf("jwk 缺少 %s（公钥必须内嵌在 header 里）", f)
		}
	}
	if header.JWK["d"] != "" {
		t.Error("jwk 绝不能包含私钥分量 d")
	}

	pb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var payload struct {
		Htm string `json:"htm"`
		Htu string `json:"htu"`
		Iat int64  `json:"iat"`
		Jti string `json:"jti"`
	}
	if err := json.Unmarshal(pb, &payload); err != nil {
		t.Fatalf("payload 解析失败: %v", err)
	}
	if payload.Htm != "POST" || payload.Htu != htu {
		t.Errorf("htm/htu = %s/%s, want POST/%s", payload.Htm, payload.Htu, htu)
	}
	if payload.Iat != now.Unix() {
		t.Errorf("iat = %d, want %d", payload.Iat, now.Unix())
	}
	if len(payload.Jti) != 64 {
		t.Errorf("jti 应为 64 位 hex，实际 %d 位", len(payload.Jti))
	}

	// 用公钥验证签名（曲线点数 → 64 字节 r||s）
	sigRaw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sigRaw) != 64 {
		t.Fatalf("签名长度异常: %d, err=%v", len(sigRaw), err)
	}
	r := new(big.Int).SetBytes(sigRaw[:32])
	s := new(big.Int).SetBytes(sigRaw[32:])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&kp.private.PublicKey, digest[:], r, s) {
		t.Fatal("签名无法用公钥验证")
	}

	// 低 S 归一化
	halfN := new(big.Int).Rsh(elliptic.P256().Params().N, 1)
	if s.Cmp(halfN) > 0 {
		t.Error("签名未做低 S 归一化")
	}
}

func TestDPoPJWKRoundTripAndMismatch(t *testing.T) {
	jwk, err := NewDPoPPrivateJWK()
	if err != nil {
		t.Fatal(err)
	}
	if jwk["d"] == "" {
		t.Fatal("持久化 JWK 必须带私钥分量")
	}
	if _, err := dpopKeyPairFromJWK(jwk); err != nil {
		t.Fatalf("还原失败: %v", err)
	}

	// 换掉 x 造成公私钥不匹配 → 必须报错，而不是静默签出无效 proof
	bad := DPoPPrivateJWK{"kty": jwk["kty"], "crv": jwk["crv"], "x": jwk["y"], "y": jwk["x"], "d": jwk["d"]}
	if _, err := dpopKeyPairFromJWK(bad); err == nil {
		t.Error("公私钥不匹配时必须报错")
	}

	// 曲线不对
	if _, err := dpopKeyPairFromJWK(DPoPPrivateJWK{"kty": "RSA", "crv": "P-256"}); err == nil {
		t.Error("非 EC 密钥必须报错")
	}
}

func TestSignDPoPProofUsesFreshJTI(t *testing.T) {
	jwk, _ := NewDPoPPrivateJWK()
	a, err1 := SignDPoPProof(jwk, STSHost+EpOAuthTokens)
	b, err2 := SignDPoPProof(jwk, STSHost+EpOAuthTokens)
	if err1 != nil || err2 != nil {
		t.Fatalf("签名失败: %v %v", err1, err2)
	}
	if a == b {
		t.Error("同一秒内两次 proof 的 jti 也应不同（防重放）")
	}
}
