package handler

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/dto"
)

func TestRegularUserTunnelOptionsKeepAdminListProtected(t *testing.T) {
	fx := setupFlowHandlerFixture(t, "gost")
	token, err := crypto.GenerateToken(fx.user.ID, fx.user.User, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Register(router, "", false)
	for _, tc := range []struct {
		path string
		code int
	}{{"/api/v1/tunnel/user/tunnel", 0}, {"/api/v1/tunnel/list", 403}} {
		req := httptest.NewRequest("POST", tc.path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", token)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		var body struct {
			Code int                  `json:"code"`
			Data []dto.TunnelListItem `json:"data"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Code != tc.code {
			t.Fatalf("%s: %s", tc.path, out.Body.String())
		}
		if tc.code == 0 && (len(body.Data) != 1 || body.Data[0].ID != fx.tunnel.ID) {
			t.Fatalf("options: %s", out.Body.String())
		}
	}
}
