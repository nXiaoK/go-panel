// Package subscriptionassets 内嵌默认订阅模板和脚本，
// 使面板程序与测试不依赖进程当前工作目录。
package subscriptionassets

import "embed"

// Files 保存随面板发布的默认订阅资源。
//
//go:embed clash.yml sing-box-android.json surge.config vless-server.sh
var Files embed.FS
