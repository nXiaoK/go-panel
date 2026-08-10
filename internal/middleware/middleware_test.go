package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

func TestJwtAuthRejectsDisabledUserToken(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	crypto.InitJwt("test-secret")

	token, err := crypto.GenerateToken(1, "admin_user", 0, 0)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("status", 0).Error; err != nil {
		t.Fatalf("disable user: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(JwtAuth())
	r.GET("/secure", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Authorization", token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 with result body", w.Code)
	}
	var got result.R
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Code != 403 {
		t.Fatalf("code=%d msg=%q, want 403", got.Code, got.Msg)
	}
}

func TestJwtAuthCurrentUserUsesDatabaseRole(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	crypto.InitJwt("test-secret")

	token, err := crypto.GenerateToken(1, "admin_user", 0, 0)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("role_id", 1).Error; err != nil {
		t.Fatalf("demote user: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(JwtAuth())
	r.GET("/secure", func(c *gin.Context) {
		claims := GetClaims(c)
		user := GetCurrentUser(c)
		if claims == nil || user == nil {
			c.JSON(http.StatusOK, result.Err("missing auth context"))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"tokenRole":   claims.RoleID,
			"currentRole": user.RoleID,
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Authorization", token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
	var got struct {
		TokenRole   int `json:"tokenRole"`
		CurrentRole int `json:"currentRole"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.TokenRole != 0 {
		t.Fatalf("token role=%d, want stale admin role 0", got.TokenRole)
	}
	if got.CurrentRole != 1 {
		t.Fatalf("current role=%d, want database role 1", got.CurrentRole)
	}
}

func TestJwtAuthRejectsExpiredActiveUser(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	crypto.InitJwt("test-secret")

	token, err := crypto.GenerateToken(1, "admin_user", 0, 0)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	expiredAt := time.Now().Add(-time.Minute).UnixMilli()
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("exp_time", expiredAt).Error; err != nil {
		t.Fatalf("expire user: %v", err)
	}

	got := performJwtAuthRequest(t, token)
	if got.Code != 401 {
		t.Fatalf("code=%d msg=%q, want 401", got.Code, got.Msg)
	}
}

func TestJwtAuthRejectsTokenVersionMismatch(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	crypto.InitJwt("test-secret")

	token, err := crypto.GenerateToken(1, "admin_user", 0, 0)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("token_version", 1).Error; err != nil {
		t.Fatalf("increment token version: %v", err)
	}

	got := performJwtAuthRequest(t, token)
	if got.Code != 401 {
		t.Fatalf("code=%d msg=%q, want 401", got.Code, got.Msg)
	}
}

func performJwtAuthRequest(t *testing.T, token string) result.R {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(JwtAuth())
	r.GET("/secure", func(c *gin.Context) {
		c.JSON(http.StatusOK, result.OkEmpty())
	})

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req.Header.Set("Authorization", token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 with result body", w.Code)
	}

	var got result.R
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return got
}

func TestValidateCurrentUserRejectsNilClaimsAndNilUser(t *testing.T) {
	user := &CurrentUser{ID: 1, Status: 1, TokenVersion: 2}
	if err := ValidateCurrentUser(user, nil); err != errCurrentUserTokenRevoked {
		t.Fatalf("nil claims err=%v, want revoked", err)
	}
	claims := &crypto.Claims{TokenVersion: 2}
	if err := ValidateCurrentUser(nil, claims); err != errCurrentUserTokenRevoked {
		t.Fatalf("nil user err=%v, want revoked", err)
	}
	if err := ValidateCurrentUser(user, claims); err != nil {
		t.Fatalf("matching claims: %v", err)
	}
}

func TestValidateCurrentUserLegacyJWTWithoutVer(t *testing.T) {
	// Missing "ver" unmarshals to TokenVersion 0.
	legacy := &crypto.Claims{Sub: "1", Exp: time.Now().Add(time.Hour).Unix(), TokenVersion: 0}

	userZero := &CurrentUser{ID: 1, Status: 1, TokenVersion: 0}
	if err := ValidateCurrentUser(userZero, legacy); err != nil {
		t.Fatalf("version 0 user should accept legacy token: %v", err)
	}

	userRotated := &CurrentUser{ID: 1, Status: 1, TokenVersion: 3}
	if err := ValidateCurrentUser(userRotated, legacy); err != errCurrentUserTokenRevoked {
		t.Fatalf("nonzero version should reject legacy token: %v", err)
	}
}
