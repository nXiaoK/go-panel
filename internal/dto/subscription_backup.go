package dto

import "encoding/json"

// ProxyNodeBackupDto 是节点脚本的恢复快照，版本 1 保存完整服务端数据库和白名单文件。
// Database/Files 可能含 Reality 私钥、证书和密码，只能经订阅 API Key 鉴权存取，不能混入公开订阅。
type ProxyNodeBackupDto struct {
	Version    int               `json:"version"`
	SourceHost string            `json:"sourceHost"`
	Server     string            `json:"server"`
	Database   json.RawMessage   `json:"database"`
	Files      map[string]string `json:"files"`
}

// ProxyNodeRestoreDto 明确指定旧主机身份，不按 IP 自动匹配，避免共享 IP 或地址复用导致误恢复。
type ProxyNodeRestoreDto struct {
	SourceHost string `json:"sourceHost"`
}
