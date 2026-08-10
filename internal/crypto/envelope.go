package crypto

import (
	"encoding/json"
)

// EncryptedMessage 加密消息包装器（与 Java/节点端格式一致）
type EncryptedMessage struct {
	Encrypted bool   `json:"encrypted"`
	Data      string `json:"data"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

// DecryptIfNeeded 解析可能加密的报文：
// 是 {encrypted:true,data:...} 格式则解密返回明文，否则原样返回。
// 与 Java FlowController.decryptIfNeeded / WebSocketServer.decryptMessageIfNeeded 行为一致。
func DecryptIfNeeded(raw []byte, secret string) []byte {
	if len(raw) == 0 {
		return raw
	}
	var em EncryptedMessage
	if err := json.Unmarshal(raw, &em); err != nil || !em.Encrypted || em.Data == "" {
		return raw
	}
	c := GetOrCreate(secret)
	if c == nil {
		return raw
	}
	plain, err := c.Decrypt(em.Data)
	if err != nil {
		return raw
	}
	return plain
}

// EncryptIfPossible 尽力加密报文，失败时返回原文。
// 输出 {"encrypted":true,"data":...,"timestamp":...}
func EncryptIfPossible(message []byte, secret string, nowMillis int64) []byte {
	if secret == "" {
		return message
	}
	c := GetOrCreate(secret)
	if c == nil {
		return message
	}
	data, err := c.Encrypt(message)
	if err != nil {
		return message
	}
	out, err := json.Marshal(EncryptedMessage{Encrypted: true, Data: data, Timestamp: nowMillis})
	if err != nil {
		return message
	}
	return out
}
