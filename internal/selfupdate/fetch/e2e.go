//go:build selfupdate_e2e

package fetch

import (
	"os"
	"strings"
)

// e2eRewrite 只在 selfupdate_e2e 构建中编译：把 https://github.com/... 改写到
// GO_PANEL_E2E_RELEASE_BASE_URL（可为 http://）指向的本地假 Release 服务器，供容器端到端测试使用。
// 完整性仍由清单签名保证，测试公钥同样只能经该构建标签引入。
func e2eRewrite(upstream string) (string, bool) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("GO_PANEL_E2E_RELEASE_BASE_URL")), "/")
	path, ok := strings.CutPrefix(upstream, "https://github.com")
	if base == "" || !ok {
		return "", false
	}
	return base + path, true
}
