package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/service"
)

// authorizeProxyNodeBackup 必须置于 LimitBody 前：未鉴权请求不读取配置快照，也不暴露其内容校验结果。
func authorizeProxyNodeBackup(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !service.AuthorizeProxyNodeBackup(subscriptionAPIKey(c)) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, result.ErrCode(401, "API Key 无效"))
		return
	}
	c.Next()
}

func saveProxyNodeBackup(c *gin.Context) {
	var req dto.ProxyNodeBackupDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.SaveProxyNodeBackup(subscriptionAPIKey(c), req))
}

func restoreProxyNodeBackup(c *gin.Context) {
	var req dto.ProxyNodeRestoreDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.RestoreProxyNodeBackup(subscriptionAPIKey(c), req.SourceHost))
}

func listProxyNodeBackups(c *gin.Context) {
	c.JSON(http.StatusOK, service.ListProxyNodeBackups(subscriptionAPIKey(c)))
}
