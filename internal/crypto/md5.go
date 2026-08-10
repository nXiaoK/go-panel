package crypto

import (
	"crypto/md5"
	"encoding/hex"
)

// Md5 计算 32 位小写 MD5，与 Java Md5Util.md5 一致
func Md5(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
