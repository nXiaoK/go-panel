package nodecrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

type EncryptedMessage struct {
	Encrypted bool   `json:"encrypted"`
	Data      string `json:"data"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

type AESCrypto struct {
	gcm cipher.AEAD
}

func NewAESCrypto(secret string) (*AESCrypto, error) {
	if secret == "" {
		return nil, errors.New("secret is empty")
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

func (a *AESCrypto) Encrypt(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("data is empty")
	}
	nonce := make([]byte, a.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := a.gcm.Seal(nil, nonce, data, nil)
	return base64.StdEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

func (a *AESCrypto) Decrypt(data string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, err
	}
	ns := a.gcm.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("encrypted data is too short")
	}
	return a.gcm.Open(nil, raw[:ns], raw[ns:], nil)
}

func DecryptIfNeeded(raw []byte, secret string) []byte {
	var msg EncryptedMessage
	if err := json.Unmarshal(raw, &msg); err != nil || !msg.Encrypted || msg.Data == "" {
		return raw
	}
	c, err := NewAESCrypto(secret)
	if err != nil {
		return raw
	}
	plain, err := c.Decrypt(msg.Data)
	if err != nil {
		return raw
	}
	return plain
}

func EncryptIfPossible(raw []byte, secret string) []byte {
	c, err := NewAESCrypto(secret)
	if err != nil {
		return raw
	}
	data, err := c.Encrypt(raw)
	if err != nil {
		return raw
	}
	out, err := json.Marshal(EncryptedMessage{
		Encrypted: true,
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	})
	if err != nil {
		return raw
	}
	return out
}
