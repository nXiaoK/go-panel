//go:build selfupdate_e2e

package trust

import (
	"crypto/ed25519"
	"os"

	"github.com/nXiaoK/go-panel/internal/selfupdate/manifest"
)

// extraKeys 只在 selfupdate_e2e 构建标签下编译：从 GO_PANEL_E2E_TRUSTED_PUBKEY 读取一把测试公钥，
// 供容器端到端测试签发测试版本。正式发布不带此标签，CI 会检查构建命令中不出现它。
func extraKeys() []ed25519.PublicKey {
	encoded := os.Getenv("GO_PANEL_E2E_TRUSTED_PUBKEY")
	if encoded == "" {
		return nil
	}
	key, err := manifest.ParsePublicKey(encoded)
	if err != nil {
		return nil
	}
	return []ed25519.PublicKey{key}
}
