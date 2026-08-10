package crypto

import (
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// 密码哈希：新密码统一使用 bcrypt 存储；
// 历史数据为 32 位小写 MD5（与 Java 版兼容），校验时自动识别，
// 登录成功后由业务层升级为 bcrypt。

// HashPassword 生成 bcrypt 哈希
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// IsBcryptHash 判断存储值是否为 bcrypt 哈希
func IsBcryptHash(stored string) bool {
	return strings.HasPrefix(stored, "$2a$") || strings.HasPrefix(stored, "$2b$") || strings.HasPrefix(stored, "$2y$")
}

// VerifyPassword 校验明文密码与存储哈希（bcrypt 或历史 MD5）是否匹配
func VerifyPassword(stored, plain string) bool {
	if stored == "" {
		return false
	}
	if IsBcryptHash(stored) {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(plain)) == nil
	}
	return stored == Md5(plain)
}
