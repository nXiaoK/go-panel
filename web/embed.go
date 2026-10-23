// Package web 嵌入前端构建产物（vite-frontend 的 dist）。
package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	pathpkg "path"
	"strconv"
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
	registerFS(r, sub)
}

func registerFS(r *gin.Engine, sub fs.FS) {
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
				if strings.HasPrefix(path, "/assets/") {
					// Vite 哈希资源可缓存一年；HTML 入口仍不缓存，发布后会引用新的哈希。
					c.Header("Cache-Control", "public, max-age=31536000, immutable")
					appendVary(c.Writer.Header(), "Accept-Encoding")
					if encoding, suffix := selectPrecompressed(sub, trimmed, c.Request.Header.Get("Accept-Encoding")); suffix != "" {
						c.Header("Content-Encoding", encoding)
						if contentType := mime.TypeByExtension(pathpkg.Ext(trimmed)); contentType != "" {
							c.Header("Content-Type", contentType)
						}
						request := c.Request.Clone(c.Request.Context())
						request.URL.Path = path + suffix
						fileServer.ServeHTTP(c.Writer, request)
						return
					}
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

// appendVary 不覆盖 CORS 等上游已有的 Vary 字段。
func appendVary(header http.Header, value string) {
	for _, line := range header.Values("Vary") {
		for _, token := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(token), value) || strings.TrimSpace(token) == "*" {
				return
			}
		}
	}
	header.Add("Vary", value)
}

// selectPrecompressed 按客户端权重协商；相同权重优先 br，显式 q=0 永远不启用。
func selectPrecompressed(files fs.FS, name, accept string) (string, string) {
	weights := make(map[string]float64)
	for _, part := range strings.Split(accept, ",") {
		fields := strings.Split(part, ";")
		encoding := strings.ToLower(strings.TrimSpace(fields[0]))
		weight := 1.0
		for _, parameter := range fields[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && strings.EqualFold(key, "q") {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || !(parsed >= 0 && parsed <= 1) {
					weight = 0
				} else {
					weight = parsed
				}
			}
		}
		weights[encoding] = weight
	}
	selected, suffix, best := "", "", 0.0
	for _, candidate := range []struct{ encoding, suffix string }{{"br", ".br"}, {"gzip", ".gz"}} {
		weight, specified := weights[candidate.encoding]
		if !specified {
			weight = weights["*"]
		}
		if weight <= best {
			continue
		}
		if info, err := fs.Stat(files, name+candidate.suffix); err == nil && info.Mode().IsRegular() {
			selected, suffix, best = candidate.encoding, candidate.suffix, weight
		}
	}
	return selected, suffix
}
