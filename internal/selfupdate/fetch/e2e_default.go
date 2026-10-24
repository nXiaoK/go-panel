//go:build !selfupdate_e2e

package fetch

// e2eRewrite 在正式构建中恒不生效：更新只能从 https://github.com（或配置的代理）下载。
func e2eRewrite(string) (string, bool) { return "", false }
