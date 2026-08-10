package handler

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/service"
)

func subStoreMethodNotAllowed(c *gin.Context) {
	c.Header("Allow", http.MethodPost)
	c.JSON(http.StatusMethodNotAllowed, result.Err("请使用 POST 请求或订阅 token"))
}

// subStore POST /api/v1/open_api/sub_store 返回订阅流量头（subscription-userinfo）。
func subStore(c *gin.Context) {
	var body struct {
		User   string `json:"user"`
		Pwd    string `json:"pwd"`
		Tunnel string `json:"tunnel"`
	}
	if !bindJSON(c, &body) {
		return
	}
	if body.User == "" {
		c.JSON(http.StatusOK, result.Err("用户不能为空"))
		return
	}
	if body.Pwd == "" {
		c.JSON(http.StatusOK, result.Err("密码不能为空"))
		return
	}
	tunnel := body.Tunnel
	if tunnel == "" {
		tunnel = "-1"
	}

	userInfo, err := service.AuthenticateCredentials(body.User, body.Pwd, c.ClientIP())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAttemptLimited):
			c.JSON(http.StatusOK, result.Err("登录尝试过多，请稍后重试"))
		case errors.Is(err, service.ErrAccountDisabled):
			c.JSON(http.StatusOK, result.Err("账号已被禁用"))
		case errors.Is(err, service.ErrAccountExpired):
			c.JSON(http.StatusOK, result.Err("账号已到期"))
		case errors.Is(err, service.ErrCredentialStore):
			c.JSON(http.StatusOK, result.Err("认证服务暂不可用，请稍后重试"))
		default:
			c.JSON(http.StatusOK, result.Err("鉴权失败"))
		}
		return
	}
	now := time.Now().UnixMilli()

	const giga = int64(1024 * 1024 * 1024)
	var headerValue string

	if tunnel == "-1" {
		expTime := int64(0)
		if userInfo.ExpTime != nil {
			expTime = *userInfo.ExpTime / 1000
		}
		headerValue = buildSubscriptionHeader(userInfo.OutFlow, userInfo.InFlow, userInfo.Flow*giga, expTime)
	} else {
		var ut model.UserTunnel
		if err := model.DB.First(&ut, tunnel).Error; err != nil {
			c.JSON(http.StatusOK, result.Err("隧道不存在"))
			return
		}
		if ut.UserID != userInfo.ID {
			c.JSON(http.StatusOK, result.Err("隧道不存在"))
			return
		}
		if ut.Status != 1 {
			c.JSON(http.StatusOK, result.Err("隧道被禁用"))
			return
		}
		if ut.ExpTime != nil && *ut.ExpTime <= now {
			c.JSON(http.StatusOK, result.Err("隧道权限已到期"))
			return
		}
		expTime := int64(0)
		if ut.ExpTime != nil {
			expTime = *ut.ExpTime / 1000
		}
		headerValue = buildSubscriptionHeader(ut.OutFlow, ut.InFlow, ut.Flow*giga, expTime)
	}

	c.Header("subscription-userinfo", headerValue)
	c.String(http.StatusOK, headerValue)
}

// buildSubscriptionHeader 与 Java 版一致：upload 字段取 download 参数（保持原行为）
func buildSubscriptionHeader(upload, download, total, expire int64) string {
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", download, upload, total, expire)
}
