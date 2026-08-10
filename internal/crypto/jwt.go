package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// JWT 实现，与 Java JwtUtil 格式兼容：
// header: {"alg":"HmacSHA256","typ":"JWT"}
// payload: {sub, iat, exp, user, name, role_id, ver}
// 签名: HMAC-SHA256(base64url(header)+"."+base64url(payload))，base64url 无填充

var jwtSecret []byte

// token 有效期 90 天（与 Java 版 EXPIRE_TIME 一致）
const jwtExpire = 90 * 24 * time.Hour

// InitJwt 设置签名密钥
func InitJwt(secret string) {
	jwtSecret = []byte(secret)
}

func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func sign(headerAndPayload string) string {
	mac := hmac.New(sha256.New, jwtSecret)
	mac.Write([]byte(headerAndPayload))
	return b64url(mac.Sum(nil))
}

// GenerateToken 生成 JWT，userID/userName/roleID 与 Java 版 payload 字段一致
func GenerateToken(userID int64, userName string, roleID int, tokenVersion int64) (string, error) {
	header := map[string]interface{}{"alg": "HmacSHA256", "typ": "JWT"}
	now := time.Now()
	payload := map[string]interface{}{
		"sub":     strconv.FormatInt(userID, 10),
		"iat":     now.Unix(),
		"exp":     now.Add(jwtExpire).Unix(),
		"user":    userName,
		"name":    userName,
		"role_id": roleID,
		"ver":     tokenVersion,
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	hp := b64url(hb) + "." + b64url(pb)
	return hp + "." + sign(hp), nil
}

// Claims JWT 载荷
type Claims struct {
	Sub          string `json:"sub"`
	Iat          int64  `json:"iat"`
	Exp          int64  `json:"exp"`
	User         string `json:"user"`
	Name         string `json:"name"`
	RoleID       int    `json:"role_id"`
	TokenVersion int64  `json:"ver"`
}

// UserID 解析 sub 为整数
func (c *Claims) UserID() int64 {
	id, _ := strconv.ParseInt(c.Sub, 10, 64)
	return id
}

// ParseToken 校验签名与过期时间并返回 Claims
func ParseToken(token string) (*Claims, error) {
	if token == "" {
		return nil, errors.New("token 为空")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("token 格式错误")
	}
	if !hmac.Equal([]byte(sign(parts[0]+"."+parts[1])), []byte(parts[2])) {
		return nil, errors.New("token 签名校验失败")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("token payload 解码失败")
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errors.New("token payload 解析失败")
	}
	if claims.Exp <= time.Now().Unix() {
		return nil, errors.New("token 已过期")
	}
	return &claims, nil
}

// ValidateToken 仅校验有效性
func ValidateToken(token string) bool {
	_, err := ParseToken(token)
	return err == nil
}
