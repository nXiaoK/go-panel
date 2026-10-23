package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

func TestCheckNodeStatusRequiresNodeID(t *testing.T) {
	for _, body := range []string{`{}`, `{"nodeId":0}`, `{"nodeId":-1}`, `{"nodeId":null}`, `{"nodeId":"1"}`, `[]`, ``} {
		t.Run(body, func(t *testing.T) {
			router := gin.New()
			router.POST("/node/check-status", checkNodeStatus)
			req := httptest.NewRequest(http.MethodPost, "/node/check-status", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			var got result.R
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Code == 0 || got.Data != nil {
				t.Fatalf("invalid request returned node data: %s", rec.Body.String())
			}
		})
	}
}

func TestCheckNodeStatusReturnsOnlyRequestedNode(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	for _, name := range []string{"requested", "other"} {
		if err := model.DB.Create(&model.Node{Name: name, Secret: name, ServerIP: "192.0.2.1", Status: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	router.POST("/node/check-status", checkNodeStatus)
	req := httptest.NewRequest(http.MethodPost, "/node/check-status", strings.NewReader(`{"nodeId":1}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var got struct {
		Code int                 `json:"code"`
		Data dto.NodeStatusCheck `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Code != 0 || got.Data.NodeID != 1 || got.Data.Name != "requested" {
		t.Fatalf("response=%s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "other") || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("unexpected node data: %s", rec.Body.String())
	}
}
