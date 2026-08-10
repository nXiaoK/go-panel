package middleware

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

const (
	// CtxClaims gin context 中存放 JWT Claims 的 key
	CtxClaims = "claims"
	// CtxUser gin context 中存放数据库当前用户快照的 key
	CtxUser = "currentUser"
)

var (
	errCurrentUserDisabled     = errors.New("current user is disabled")
	errCurrentUserExpired      = errors.New("current user is expired")
	errCurrentUserTokenRevoked = errors.New("current user token is revoked")
)

// CurrentUser 是鉴权后从数据库读取的当前用户快照。
type CurrentUser struct {
	ID            int64
	User          string
	RoleID        int
	Status        int
	ExpTime       *int64
	TokenVersion  int64
	MustChangePwd int
}

func activeUserStatus(status int) bool {
	return model.IsActiveUserStatus(status)
}

// LoadCurrentUser loads the authentication fields shared by HTTP and WebSocket authorization.
func LoadCurrentUser(userID int64) (*CurrentUser, error) {
	var user model.User
	if err := model.DB.Select("id, user, role_id, status, exp_time, token_version, must_change_pwd").First(&user, userID).Error; err != nil {
		return nil, err
	}
	return &CurrentUser{
		ID:            user.ID,
		User:          user.User,
		RoleID:        user.RoleID,
		Status:        user.Status,
		ExpTime:       user.ExpTime,
		TokenVersion:  user.TokenVersion,
		MustChangePwd: user.MustChangePwd,
	}, nil
}

// ValidateCurrentUser applies account lifecycle and token revocation checks.
// Both user and claims are required; a nil claims pointer is treated as revoked
// so callers cannot accidentally skip version checks.
func ValidateCurrentUser(user *CurrentUser, claims *crypto.Claims) error {
	if user == nil || claims == nil {
		return errCurrentUserTokenRevoked
	}
	if !activeUserStatus(user.Status) {
		return errCurrentUserDisabled
	}
	if user.ExpTime != nil && *user.ExpTime <= time.Now().UnixMilli() {
		return errCurrentUserExpired
	}
	if claims.TokenVersion != user.TokenVersion {
		return errCurrentUserTokenRevoked
	}
	return nil
}

func enforceCurrentUser(c *gin.Context, user *CurrentUser, claims *crypto.Claims) bool {
	switch err := ValidateCurrentUser(user, claims); err {
	case nil:
		return true
	case errCurrentUserDisabled:
		c.AbortWithStatusJSON(http.StatusOK, result.ErrCode(403, "账号已被禁用"))
	case errCurrentUserExpired:
		c.AbortWithStatusJSON(http.StatusOK, result.ErrCode(401, "账号已过期"))
	default:
		c.AbortWithStatusJSON(http.StatusOK, result.ErrCode(401, "token已失效"))
	}
	return false
}

func getOrLoadCurrentUser(c *gin.Context) (*CurrentUser, bool) {
	if user := GetCurrentUser(c); user != nil {
		return user, true
	}
	claims := GetClaims(c)
	if claims == nil {
		return nil, false
	}
	user, err := LoadCurrentUser(claims.UserID())
	if err != nil {
		c.AbortWithStatusJSON(http.StatusOK, result.ErrCode(401, "用户不存在"))
		return nil, false
	}
	if !enforceCurrentUser(c, user, claims) {
		return nil, false
	}
	c.Set(CtxUser, user)
	return user, true
}

// JwtAuth 校验 Authorization 头中的 JWT（对应 Java JwtInterceptor）。
// 失败时返回 {code:401,...}，与 GlobalExceptionHandler 对 UnauthorizedException 的行为一致。
func JwtAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token == "" {
			c.AbortWithStatusJSON(http.StatusOK, result.ErrCode(401, "未登录或token已过期"))
			return
		}
		claims, err := crypto.ParseToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusOK, result.ErrCode(401, "无效的token或token已过期"))
			return
		}
		user, err := LoadCurrentUser(claims.UserID())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusOK, result.ErrCode(401, "用户不存在"))
			return
		}
		if !enforceCurrentUser(c, user, claims) {
			return
		}
		c.Set(CtxClaims, claims)
		c.Set(CtxUser, user)
		c.Next()
	}
}

// RequireAdmin 管理员权限校验（对应 @RequireRole / RoleAspect，role_id=0 为管理员）
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := getOrLoadCurrentUser(c)
		if !ok {
			if !c.IsAborted() {
				c.AbortWithStatusJSON(http.StatusOK, result.ErrCode(401, "无法获取用户权限信息"))
			}
			return
		}
		if user.RoleID != 0 {
			c.AbortWithStatusJSON(http.StatusOK, result.ErrCode(403, "权限不足，仅管理员可操作"))
			return
		}
		c.Next()
	}
}

// RequirePasswordChanged 强制改密校验：must_change_pwd=1 的用户除改密接口外一律拒绝。
// 防止默认管理员/历史 MD5 账号在未改密前调用业务接口。
func RequirePasswordChanged() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := getOrLoadCurrentUser(c)
		if !ok {
			if c.IsAborted() {
				return
			}
			c.Next()
			return
		}
		if user.MustChangePwd == 1 {
			c.AbortWithStatusJSON(http.StatusOK, result.ErrCode(403, "请先修改默认密码"))
			return
		}
		c.Next()
	}
}

// GetClaims 从 context 取出 JWT Claims
func GetClaims(c *gin.Context) *crypto.Claims {
	if v, ok := c.Get(CtxClaims); ok {
		if claims, ok := v.(*crypto.Claims); ok {
			return claims
		}
	}
	return nil
}

// GetCurrentUser 从 context 取出数据库当前用户快照。
func GetCurrentUser(c *gin.Context) *CurrentUser {
	if v, ok := c.Get(CtxUser); ok {
		if user, ok := v.(*CurrentUser); ok {
			return user
		}
	}
	return nil
}

// Cors 跨域中间件（对应 WebMvcConfig CorsFilter）。
// allowOrigin 为允许的来源，空串表示 *（兼容默认行为）；
// 指定具体来源时按请求 Origin 回显并加 Vary 头。
func Cors(allowOrigin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if allowOrigin == "" || allowOrigin == "*" {
			c.Header("Access-Control-Allow-Origin", "*")
		} else {
			origin := c.GetHeader("Origin")
			if origin != "" && originAllowed(origin, allowOrigin) {
				c.Header("Access-Control-Allow-Origin", origin)
			}
			c.Header("Vary", "Origin")
		}
		// 显式列出允许的请求头，避免使用通配 * 提高可探测性
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key, X-Node-Secret")
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, PUT, OPTIONS")
		c.Header("Access-Control-Expose-Headers", "Authorization, Content-Disposition")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// originAllowed 判断请求 Origin 是否在逗号分隔的允许列表中
func originAllowed(origin, allowList string) bool {
	for _, allowed := range strings.Split(allowList, ",") {
		if strings.EqualFold(strings.TrimSpace(allowed), origin) {
			return true
		}
	}
	return false
}

// DatabaseOperationScope enters the process operation gate around a request so
// database restore can drain in-flight work. While a maintenance window (DB
// restore) is active or pending, new requests receive a retryable 503 instead
// of racing the handle swap. The restore route itself must NOT use this.
func DatabaseOperationScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		leave, ok := model.Gate.Enter()
		if !ok {
			c.Header("Retry-After", "3")
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, result.ErrCode(503, "系统维护中，请稍后重试"))
			return
		}
		defer leave()
		c.Next()
	}
}
