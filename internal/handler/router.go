package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/middleware"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/service"
	"github.com/nXiaoK/go-panel/internal/ws"
)

// Register 注册全部路由；corsAllowOrigin 为空表示允许所有来源
func Register(r *gin.Engine, corsAllowOrigin string, allowLegacyNftReports bool) {
	r.Use(middleware.Cors(corsAllowOrigin))
	ws.SetAllowedOrigins(corsAllowOrigin)

	// 容器健康检查只确认 HTTP 进程可用，不公开版本、配置或数据库内容。
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// WebSocket（节点 + 管理员）
	r.GET("/system-info", func(c *gin.Context) {
		ws.HandleSystemInfo(c.Writer, c.Request)
	})

	// 节点流量上报（secret 鉴权，无 JWT）。数据库恢复维护期间返回可重试 503。
	flow := r.Group("/flow", middleware.DatabaseOperationScope())
	{
		flow.POST("/upload", uploadFlowData)
		flow.Any("/test", func(c *gin.Context) { c.String(http.StatusOK, "test") })
		flow.POST("/nft-upload", func(c *gin.Context) {
			if !allowLegacyNftReports {
				flowError(c, http.StatusUpgradeRequired, "legacy nft reports disabled")
				return
			}
			uploadNftFlowBatch(c)
		})
		flow.POST("/nft-upload-v2", uploadNftFlowBatchV2)
		flow.POST("/config", uploadGostConfig)
	}

	apiRoot := r.Group("/api/v1")
	// 数据库操作门控：恢复期间新请求收到可重试 503；/backup/restore 自身
	// 走 restoreAdmin（无此中间件），由服务层直接进入维护窗口。
	api := apiRoot.Group("", LimitBody(maxJSONBodySize), middleware.DatabaseOperationScope())

	// 免鉴权端点
	api.POST("/user/login", login)
	api.POST("/config/get", getConfigByName)
	api.GET("/open_api/sub_store", subStoreMethodNotAllowed)
	api.POST("/open_api/sub_store", subStore)
	api.GET("/node/nft-config", nftConfig)
	api.GET("/node/install/:name", nodeInstallScript)
	api.GET("/node/assets/:name", nodeBinaryAsset)
	api.POST("/sub/node/report", reportProxyNode)
	api.POST("/sub/node/delete-report", deleteReportedProxyNode)
	api.GET("/sub/render/:token", publicSubscription)
	api.GET("/sub/render/:token/:format", publicSubscription)
	api.GET("/sub/vless-server.sh", vlessServerScript)

	// 需要 JWT 的端点
	// authOnly 仅校验 JWT，不强制改密——供 updatePassword 自解锁使用
	authOnly := api.Group("", middleware.JwtAuth())
	// authChecked 在 JWT 基础上额外强制改密（must_change_pwd=1 拒绝）
	authChecked := api.Group("", middleware.JwtAuth(), middleware.RequirePasswordChanged())
	admin := authChecked.Group("", middleware.RequireAdmin())
	restoreAdmin := apiRoot.Group("", middleware.JwtAuth(), middleware.RequirePasswordChanged(), middleware.RequireAdmin())

	// user
	admin.POST("/user/create", createUser)
	admin.POST("/user/list", listUsers)
	admin.POST("/user/update", updateUser)
	admin.POST("/user/delete", deleteUser)
	admin.POST("/user/reset", resetFlow)
	authChecked.POST("/user/package", userPackage)
	// updatePassword 放在 authOnly，使被强制改密锁定的用户仍可改密解锁
	authOnly.POST("/user/updatePassword", updatePassword)

	// node
	admin.POST("/node/create", createNode)
	admin.POST("/node/list", listNodes)
	admin.POST("/node/update", updateNode)
	admin.POST("/node/delete", deleteNode)
	admin.POST("/node/install", nodeInstall)
	admin.POST("/node/uninstall", nodeUninstall)
	admin.POST("/node/upgrade", nodeUpgrade)
	admin.POST("/node/check-status", checkNodeStatus)

	// tunnel
	admin.POST("/tunnel/create", createTunnel)
	admin.POST("/tunnel/list", listTunnels)
	admin.POST("/tunnel/get", getTunnel)
	admin.POST("/tunnel/update", updateTunnel)
	admin.POST("/tunnel/delete", deleteTunnel)
	admin.POST("/tunnel/diagnose", diagnoseTunnel)
	admin.POST("/tunnel/speed-test", speedTestTunnel)
	admin.POST("/tunnel/user/assign", assignUserTunnel)
	admin.POST("/tunnel/user/list", listUserTunnels)
	admin.POST("/tunnel/user/remove", removeUserTunnel)
	admin.POST("/tunnel/user/update", updateUserTunnel)
	authChecked.POST("/tunnel/user/tunnel", userTunnelOptions)

	// forward
	authChecked.POST("/forward/create", createForward)
	authChecked.POST("/forward/list", listForwards)
	authChecked.POST("/forward/update", updateForward)
	authChecked.POST("/forward/delete", deleteForward)
	authChecked.POST("/forward/force-delete", forceDeleteForward)
	authChecked.POST("/forward/pause", pauseForward)
	authChecked.POST("/forward/resume", resumeForward)
	authChecked.POST("/forward/diagnose", diagnoseForward)
	authChecked.POST("/forward/update-order", updateForwardOrder)

	// NFT 规则识别和补全（仅管理员）
	admin.POST("/forward/detect-nft-rules", detectNftRules)
	admin.POST("/forward/detect-tunnel-rules", detectTunnelRules)
	admin.POST("/forward/complete-from-nft", completeFromNft)

	// speed-limit
	admin.POST("/speed-limit/create", createSpeedLimit)
	admin.POST("/speed-limit/list", listSpeedLimits)
	admin.POST("/speed-limit/update", updateSpeedLimit)
	admin.POST("/speed-limit/delete", deleteSpeedLimit)
	admin.POST("/speed-limit/tunnels", listTunnels)

	// config（list 按角色过滤，update 需管理员，get 仅公开白名单）
	authChecked.POST("/config/list", listConfigs)
	admin.POST("/config/update", updateConfigs)
	admin.POST("/config/update-single", updateSingleConfig)

	// system
	authChecked.POST("/system/status", systemStatus)
	authChecked.POST("/system/version", systemVersion)
	authChecked.POST("/system/update/check", systemUpdateCheck)
	admin.POST("/system/update/apply", systemUpdateApply)

	// backup
	admin.GET("/backup/download", downloadBackup)
	restoreAdmin.POST("/backup/restore", restoreBackup)
	admin.POST("/backup/r2/settings", getR2BackupSettings)
	admin.POST("/backup/r2/update", updateR2BackupSettings)
	admin.POST("/backup/r2/test", testR2BackupConnection)
	admin.POST("/backup/r2/run", runR2BackupNow)
	admin.POST("/backup/detect-extra-rules", detectExtraRules)
	admin.POST("/backup/handle-extra-rules", handleExtraRules)

	// subscription
	admin.POST("/sub/settings", subscriptionSettings)
	admin.POST("/sub/api-key", updateSubscriptionAPIKey)
	admin.POST("/sub/node/list", listProxyNodes)
	admin.POST("/sub/node/update", updateProxyNode)
	admin.POST("/sub/node/delete", deleteProxyNode)
	admin.POST("/sub/node/assign-profiles", assignProxyNodeProfiles)
	admin.POST("/sub/node/relay", createProxyNodeRelay)
	admin.POST("/sub/node/relay/preview", previewProxyNodeRelay)
	admin.POST("/sub/node/relay/close", closeProxyNodeRelay)
	admin.POST("/sub/node/import", importProxyLink)
	admin.POST("/sub/profile/create", createSubscriptionProfile)
	admin.POST("/sub/profile/update", updateSubscriptionProfile)
	admin.POST("/sub/profile/delete", deleteSubscriptionProfile)
	admin.POST("/sub/profile/token", regenerateSubscriptionToken)
	admin.POST("/sub/preview", previewSubscription)
}

// ===== 通用工具 =====

// currentUser 从鉴权中间件保存的数据库用户快照构造业务用户上下文
func currentUser(c *gin.Context) service.CurrentUser {
	user := middleware.GetCurrentUser(c)
	if user != nil {
		return service.CurrentUser{
			UserID:   user.ID,
			RoleID:   user.RoleID,
			UserName: user.User,
		}
	}
	if claims := middleware.GetClaims(c); claims != nil {
		return service.CurrentUser{
			UserID:   claims.UserID(),
			RoleID:   1,
			UserName: claims.Name,
		}
	}
	return service.CurrentUser{RoleID: 1}
}

// idFromBody 解析 {"id": ...}
func idFromBody(c *gin.Context, key string) (int64, bool) {
	var params map[string]interface{}
	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(http.StatusOK, result.Err("参数错误"))
		return 0, false
	}
	v, ok := params[key]
	if !ok {
		c.JSON(http.StatusOK, result.Err("缺少"+key+"参数"))
		return 0, false
	}
	id, ok := toInt64(v)
	if !ok {
		c.JSON(http.StatusOK, result.Err(key+"参数格式错误"))
		return 0, false
	}
	return id, true
}

func toInt64(v interface{}) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int64:
		return x, true
	case int:
		return int64(x), true
	case string:
		id, err := strconv.ParseInt(x, 10, 64)
		return id, err == nil
	}
	return 0, false
}
