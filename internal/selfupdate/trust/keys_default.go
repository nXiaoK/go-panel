//go:build !selfupdate_e2e

package trust

import "crypto/ed25519"

// extraKeys 在正式构建中恒为空：发布公钥只能来自 releaseKeys。
func extraKeys() []ed25519.PublicKey { return nil }
