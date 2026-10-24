// Package trust 保存允许签发更新清单的发布公钥。
//
// supervisor 只执行能用这些公钥验证签名的产物，因此这里是软更新的信任根。
// 修改本文件必须经过代码审查；生产代码中不存在任何运行时覆盖手段。
package trust

import (
	"crypto/ed25519"

	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
)

// releaseKeys 是 base64 编码的 Ed25519 发布公钥，由维护者按 RELEASING.md 生成后追加。
//
// 列表为空时任何清单都无法通过验证，软更新保持关闭（失败关闭）。
// 轮换：新旧公钥至少共存一个发布周期，期间 CI 用两把私钥双签；
// 确认所有在用镜像都已内置新公钥后，才能删除旧公钥。
var releaseKeys = []string{}

// Default 返回内置的受信任公钥。
func Default() manifest.KeyRing {
	keys := make([]ed25519.PublicKey, 0, len(releaseKeys)+1)
	for _, encoded := range releaseKeys {
		// 无效的公钥由 TestReleaseKeysAreValid 拦截；这里跳过而不是 panic，避免面板无法启动。
		if key, err := manifest.ParsePublicKey(encoded); err == nil {
			keys = append(keys, key)
		}
	}
	keys = append(keys, extraKeys()...)
	return manifest.NewKeyRing(keys...)
}

// Configured 报告是否至少内置了一把有效的发布公钥。
func Configured() bool {
	return Default().Len() > 0
}
