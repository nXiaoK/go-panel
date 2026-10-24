//go:build !selfupdate_e2e

package trust

import "testing"

func TestProductionBuildIgnoresE2EKeyVariable(t *testing.T) {
	// 32 字节全零的合法 Ed25519 公钥编码：即便格式正确，正式构建也不得因环境变量增加信任。
	t.Setenv("GO_PANEL_E2E_TRUSTED_PUBKEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if got, want := Default().Len(), len(releaseKeys); got != want {
		t.Fatalf("an environment variable must never add trusted keys in production builds: %d keys, want %d", got, want)
	}
}
