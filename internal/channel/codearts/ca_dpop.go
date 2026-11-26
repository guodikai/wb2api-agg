// dpop.go DPoP（RFC 9449）证明 JWT：ES256 + P-256，供华为云 STS 的
// oauth2/tokens 请求头使用。
//
// 移植来源：HITZY2002/codearts2api internal/upstream/dpop.go（MIT）。
// 关键事实：华为 STS 把 refresh_token 绑定到首次换取时的 DPoP 公钥——
// **私钥必须持久化并在续期时复用**，换了就打不动（InvalidDPoPHeader）。
package codearts

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"time"
)

// DPoPPrivateJWK 可持久化的 DPoP 私钥（含私钥分量 d）。
type DPoPPrivateJWK map[string]string

type dpopKeyPair struct {
	private *ecdsa.PrivateKey
	public  map[string]string
}

// NewDPoPPrivateJWK 生成一对新的 DPoP 私钥（登录时调用一次，之后长期复用）。
func NewDPoPPrivateJWK() (DPoPPrivateJWK, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return DPoPPrivateJWK{
		"kty": "EC",
		"crv": "P-256",
		"x":   b64(padded(key.PublicKey.X, 32)),
		"y":   b64(padded(key.PublicKey.Y, 32)),
		"d":   b64(padded(key.D, 32)),
	}, nil
}

// dpopKeyPairFromJWK 从持久化私钥还原密钥对，并校验公私钥一致
// （防止文件被改坏后一直签名失败、却以为是对端问题）。
func dpopKeyPairFromJWK(jwk DPoPPrivateJWK) (*dpopKeyPair, error) {
	if jwk["kty"] != "EC" || jwk["crv"] != "P-256" {
		return nil, fmt.Errorf("DPoP JWK 曲线非法")
	}
	decode := func(field string) ([]byte, error) {
		raw, err := base64.RawURLEncoding.DecodeString(jwk[field])
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("DPoP JWK %s 非法", field)
		}
		return raw, nil
	}
	xRaw, err := decode("x")
	if err != nil {
		return nil, err
	}
	yRaw, err := decode("y")
	if err != nil {
		return nil, err
	}
	dRaw, err := decode("d")
	if err != nil {
		return nil, err
	}
	curve := elliptic.P256()
	x, y, d := new(big.Int).SetBytes(xRaw), new(big.Int).SetBytes(yRaw), new(big.Int).SetBytes(dRaw)
	if d.Sign() <= 0 || d.Cmp(curve.Params().N) >= 0 || !curve.IsOnCurve(x, y) {
		return nil, fmt.Errorf("DPoP JWK 密钥材料非法")
	}
	wantX, wantY := curve.ScalarBaseMult(dRaw)
	if wantX.Cmp(x) != 0 || wantY.Cmp(y) != 0 {
		return nil, fmt.Errorf("DPoP JWK 公私钥不匹配")
	}
	return &dpopKeyPair{
		private: &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y}, D: d},
		public:  map[string]string{"kty": "EC", "crv": "P-256", "x": jwk["x"], "y": jwk["y"]},
	}, nil
}

// SignDPoPProof 生成 htm=POST、htu=目标 token 端点的 DPoP 证明。
func SignDPoPProof(jwk DPoPPrivateJWK, htu string) (string, error) {
	kp, err := dpopKeyPairFromJWK(jwk)
	if err != nil {
		return "", err
	}
	return signDPoPProofAt(kp, htu, time.Now())
}

// signDPoPProofAt 时间可注入，便于测试断言 iat。
func signDPoPProofAt(kp *dpopKeyPair, htu string, now time.Time) (string, error) {
	header := map[string]any{"alg": "ES256", "typ": "dpop+jwt", "jwk": kp.public}
	headerJSON, _ := json.Marshal(header)
	payload := map[string]any{
		"htm": "POST",
		"htu": htu,
		"iat": now.Unix(),
		"jti": randomHexLower(32),
	}
	payloadJSON, _ := json.Marshal(payload)
	input := b64(headerJSON) + "." + b64(payloadJSON)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, kp.private, digest[:])
	if err != nil {
		return "", err
	}
	// 低 S 归一化：jose 默认低 S，服务端兼容性更好。
	n := elliptic.P256().Params().N
	halfN := new(big.Int).Rsh(n, 1)
	if s.Cmp(halfN) > 0 {
		s.Sub(n, s)
	}
	sig := append(padded(r, 32), padded(s, 32)...)
	return input + "." + b64(sig), nil
}

func padded(b *big.Int, size int) []byte {
	out := make([]byte, size)
	raw := b.Bytes()
	if len(raw) > size {
		raw = raw[len(raw)-size:]
	}
	copy(out[size-len(raw):], raw)
	return out
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func randomHexLower(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}
