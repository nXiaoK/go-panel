package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/dto"
)

func TestUserTunnelUpdateBindingRejectsStatusOutsideBooleanDomain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest("POST", "/user-tunnel/update", strings.NewReader(`{"id":1,"flow":1,"num":1,"status":2}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	var req dto.UserTunnelUpdateDto
	if bindJSON(ctx, &req) {
		t.Fatalf("binding accepted status=%v", req.Status)
	}
}
