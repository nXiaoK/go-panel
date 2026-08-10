package crypto

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMd5(t *testing.T) {
	// gost.sql 中默认管理员密码 admin_user 的 MD5
	if got := Md5("admin_user"); got != "3c85cdebade1c51cf64ca9f3c09d182d" {
		t.Fatalf("Md5(admin_user) = %s", got)
	}
}

func TestAESRoundTrip(t *testing.T) {
	c, err := NewAESCrypto("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	plain := `{"n":"1_2_3","u":100,"d":200}`
	enc, err := c.EncryptString(plain)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := c.DecryptString(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != plain {
		t.Fatalf("round trip mismatch: %s", dec)
	}
}

func TestDecryptIfNeeded(t *testing.T) {
	secret := "node-secret"
	plain := []byte(`{"hello":"world"}`)
	enc := EncryptIfPossible(plain, secret, 12345)

	var em EncryptedMessage
	if err := json.Unmarshal(enc, &em); err != nil || !em.Encrypted {
		t.Fatalf("expected encrypted envelope, got %s", enc)
	}

	got := DecryptIfNeeded(enc, secret)
	if string(got) != string(plain) {
		t.Fatalf("decrypt mismatch: %s", got)
	}

	// 明文应原样返回
	if got := DecryptIfNeeded(plain, secret); string(got) != string(plain) {
		t.Fatalf("plain passthrough mismatch: %s", got)
	}
}

func TestJwtRoundTrip(t *testing.T) {
	InitJwt("unit-test-secret")
	token, err := GenerateToken(1, "admin_user", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID() != 1 || claims.User != "admin_user" || claims.RoleID != 0 {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if !ValidateToken(token) {
		t.Fatal("token should be valid")
	}
	if ValidateToken(token + "x") {
		t.Fatal("tampered token should be invalid")
	}
}

func TestJwtCarriesTokenVersion(t *testing.T) {
	InitJwt("01234567890123456789012345678901")
	token, err := GenerateToken(7, "alice", 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ver"] != float64(4) {
		t.Fatalf("payload=%s, want ver=4", payloadJSON)
	}
	claims, err := ParseToken(token)
	if err != nil || claims.TokenVersion != 4 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
}

func TestParseTokenLegacyWithoutVerField(t *testing.T) {
	InitJwt("01234567890123456789012345678901")
	header := map[string]interface{}{"alg": "HmacSHA256", "typ": "JWT"}
	now := time.Now()
	// Deliberately omit "ver" to simulate historical tokens.
	payload := map[string]interface{}{
		"sub":     "9",
		"iat":     now.Unix(),
		"exp":     now.Add(time.Hour).Unix(),
		"user":    "legacy",
		"name":    "legacy",
		"role_id": 1,
	}
	hb, _ := json.Marshal(header)
	pb, _ := json.Marshal(payload)
	hp := b64url(hb) + "." + b64url(pb)
	token := hp + "." + sign(hp)

	claims, err := ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.TokenVersion != 0 {
		t.Fatalf("TokenVersion=%d, want 0 for missing ver", claims.TokenVersion)
	}
}
