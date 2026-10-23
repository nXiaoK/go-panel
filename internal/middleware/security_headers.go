package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders 为所有响应设置基础安全头；此阶段不启用 CSP 或 HSTS，
// 避免影响现有前端资源与 HTTP 内网部署。HSTS 应由 HTTPS 反向代理按域名配置。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		setSecurityHeaders(c.Writer.Header())
		c.Next()
	}
}

func setSecurityHeaders(header http.Header) {
	// 禁止浏览器嗅探 MIME 类型；API 和订阅仍使用各自声明的 Content-Type。
	header.Set("X-Content-Type-Options", "nosniff")
	// 禁止任何站点通过 iframe 嵌入面板，防止点击劫持。
	header.Set("X-Frame-Options", "DENY")
	// 打开外链时不发送来源，避免泄露面板地址、路径和查询参数。
	header.Set("Referrer-Policy", "no-referrer")
	// 面板无需摄像头、麦克风或定位，默认禁止这些浏览器权限。
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

// SecurityHeadersForUpgrade 只转交中间件已设置的四个基础安全头。
// gorilla 会直接写入 101 响应，不会自动发送 ResponseWriter.Header；
// 白名单避免把其他普通 HTTP 响应头混入握手，也不改动 JWT 子协议协商。
func SecurityHeadersForUpgrade(header http.Header) http.Header {
	response := make(http.Header)
	for _, name := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Permissions-Policy"} {
		if value := header.Get(name); value != "" {
			response.Set(name, value)
		}
	}
	return response
}
