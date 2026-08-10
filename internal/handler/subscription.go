package handler

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/service"
)

func subscriptionAPIKey(c *gin.Context) string {
	if key := strings.TrimSpace(c.GetHeader("X-API-Key")); key != "" {
		return key
	}
	if key := strings.TrimSpace(c.GetHeader("Authorization")); key != "" && !strings.HasPrefix(key, "Bearer ") {
		return key
	}
	return strings.TrimSpace(c.Query("apiKey"))
}

func reportProxyNode(c *gin.Context) {
	var req dto.ProxyNodeReportDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.ReportProxyNode(subscriptionAPIKey(c), req))
}

func deleteReportedProxyNode(c *gin.Context) {
	var req dto.ProxyNodeDeleteReportDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.DeleteReportedProxyNode(subscriptionAPIKey(c), req))
}

func publicSubscription(c *gin.Context) {
	token := c.Param("token")
	format := c.Param("format")
	if format == "" {
		format = c.Query("format")
	}
	body, contentType, err := service.RenderSubscription(token, format)
	if err != nil {
		c.String(http.StatusNotFound, err.Error())
		return
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "no-store")
	c.String(http.StatusOK, body)
}

func vlessServerScript(c *gin.Context) {
	data, name := service.GetVlessServerScriptContent()
	c.Header("Content-Type", "text/x-shellscript; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+filepath.Base(name)+`"`)
	c.Data(http.StatusOK, "text/x-shellscript; charset=utf-8", data)
}

func subscriptionSettings(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetSubscriptionSettings())
}

func updateSubscriptionAPIKey(c *gin.Context) {
	var req struct {
		APIKey string `json:"apiKey"`
	}
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.UpdateSubscriptionAPIKey(req.APIKey))
}

func listProxyNodes(c *gin.Context) {
	c.JSON(http.StatusOK, service.GetProxyNodes())
}

func updateProxyNode(c *gin.Context) {
	var req dto.ProxyNodeUpdateDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.UpdateProxyNode(req))
}

func deleteProxyNode(c *gin.Context) {
	var req struct {
		ID            int64 `json:"id" binding:"required"`
		DeleteForward bool  `json:"deleteForward"`
	}
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.DeleteProxyNode(currentUser(c), req.ID, req.DeleteForward))
}

func assignProxyNodeProfiles(c *gin.Context) {
	var req dto.ProxyNodeProfileAssignDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.AssignProxyNodeProfiles(req))
}

func createProxyNodeRelay(c *gin.Context) {
	var req dto.ProxyNodeRelayDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.CreateProxyNodeRelay(currentUser(c), req))
}

func previewProxyNodeRelay(c *gin.Context) {
	var req dto.ProxyNodeRelayPreviewDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.PreviewProxyNodeRelay(req))
}

func closeProxyNodeRelay(c *gin.Context) {
	var req dto.ProxyNodeRelayCloseDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.CloseProxyNodeRelay(currentUser(c), req))
}

func createSubscriptionProfile(c *gin.Context) {
	var req dto.SubscriptionProfileDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.CreateSubscriptionProfile(req))
}

func updateSubscriptionProfile(c *gin.Context) {
	var req dto.SubscriptionProfileUpdateDto
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.UpdateSubscriptionProfile(req))
}

func deleteSubscriptionProfile(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.DeleteSubscriptionProfile(id))
}

func regenerateSubscriptionToken(c *gin.Context) {
	id, ok := idFromBody(c, "id")
	if !ok {
		return
	}
	c.JSON(http.StatusOK, service.RegenerateSubscriptionToken(id))
}

func importProxyLink(c *gin.Context) {
	var req struct {
		Link string `json:"link"`
	}
	if !bindJSON(c, &req) {
		return
	}
	c.JSON(http.StatusOK, service.ImportProxyLinkForAdmin(req.Link))
}

func previewSubscription(c *gin.Context) {
	var req struct {
		Token  string `json:"token"`
		Format string `json:"format"`
	}
	if !bindJSON(c, &req) {
		return
	}
	body, _, err := service.RenderSubscription(req.Token, req.Format)
	if err != nil {
		c.JSON(http.StatusOK, result.Err(err.Error()))
		return
	}
	c.JSON(http.StatusOK, result.Ok(body))
}
