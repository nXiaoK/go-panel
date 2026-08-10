package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
)

// AESCrypto AES-256-GCM 加密器。
// 与节点端 go-gost/x/internal/util/crypto/aes.go 及 Java AESCrypto.java 完全兼容：
// key = SHA256(secret)，密文 = base64(nonce(12) + ciphertext+tag(16))
type AESCrypto struct {
	gcm cipher.AEAD
}

// NewAESCrypto 创建加密器
func NewAESCrypto(secret string) (*AESCrypto, error) {
	if secret == "" {
		return nil, errors.New("密钥不能为空")
	}
	hash := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AESCrypto{gcm: gcm}, nil
}

// Encrypt 加密并返回 base64
func (a *AESCrypto) Encrypt(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("待加密数据不能为空")
	}
	nonce := make([]byte, a.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := a.gcm.Seal(nil, nonce, data, nil)
	return base64.StdEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

// Decrypt 解密 base64 密文
func (a *AESCrypto) Decrypt(encryptedData string) ([]byte, error) {
	if encryptedData == "" {
		return nil, errors.New("加密数据不能为空")
	}
	raw, err := base64.StdEncoding.DecodeString(encryptedData)
	if err != nil {
		return nil, err
	}
	ns := a.gcm.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("加密数据长度不足")
	}
	return a.gcm.Open(nil, raw[:ns], raw[ns:], nil)
}

// EncryptString 加密字符串
func (a *AESCrypto) EncryptString(data string) (string, error) {
	return a.Encrypt([]byte(data))
}

// DecryptString 解密为字符串
func (a *AESCrypto) DecryptString(encryptedData string) (string, error) {
	b, err := a.Decrypt(encryptedData)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// cryptoCache 缓存加密器实例（对应 Java EncryptionConfig）
var cryptoCache sync.Map

// GetOrCreate 获取或创建指定 secret 的加密器，失败返回 nil
func GetOrCreate(secret string) *AESCrypto {
	if secret == "" {
		return nil
	}
	if v, ok := cryptoCache.Load(secret); ok {
		return v.(*AESCrypto)
	}
	c, err := NewAESCrypto(secret)
	if err != nil {
		return nil
	}
	actual, _ := cryptoCache.LoadOrStore(secret, c)
	return actual.(*AESCrypto)
}
