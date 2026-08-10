// Package web 嵌入前端构建产物（vite-frontend 的 dist）。
package web

import (
	"embed"
	"io/fs"
	"net/http"
	pathpkg "path"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:dist
var distFS embed.FS

// Register 注册 SPA 静态资源服务。
// 非 /api、/flow、/system-info 的路径优先匹配静态文件，否则回退到 index.html。
func Register(r *gin.Engine) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(sub))

	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		// API/WS 路径不回退到前端
		if strings.HasPrefix(path, "/api") || strings.HasPrefix(path, "/flow") || strings.HasPrefix(path, "/system-info") {
			c.JSON(http.StatusNotFound, gin.H{"code": -1, "msg": "not found"})
			return
		}

		// 尝试按静态文件提供
		trimmed := strings.TrimPrefix(path, "/")
		if trimmed != "" {
			if f, err := sub.Open(trimmed); err == nil {
				f.Close()
				if trimmed == "index.html" {
					c.Header("Cache-Control", "no-store")
				}
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}

		// 静态资源缺失时必须返回 404，不能回退到 index.html。
		// 否则浏览器会把 HTML 当作 JS/CSS 解析，导致前端卡在加载阶段。
		if strings.HasPrefix(path, "/assets/") || pathpkg.Ext(path) != "" {
			c.String(http.StatusNotFound, "not found")
			return
		}

		// SPA 回退到 index.html
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			c.String(http.StatusNotFound, "前端资源未构建：请先执行 vite-frontend 构建并将产物拷贝到 go-panel/web/dist")
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}
