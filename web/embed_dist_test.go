//go:build webdist

package web_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nXiaoK/go-panel/internal/config"
	"github.com/nXiaoK/go-panel/internal/handler"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/web"
	"golang.org/x/net/html"
)

// 此专项由 verify 在 sync-web-dist 后通过 webdist 标签强制运行。
// 普通源码检查无需预先构建前端；专项缺少真实产物会失败，不能使用测试夹具替代。
func TestBuiltIndexIsCompatibleWithStrictScriptPolicy(t *testing.T) {
	index, err := os.ReadFile("dist/index.html")
	if err != nil {
		t.Fatalf("同步后的真实 index.html 不存在: %v", err)
	}
	lower := strings.ToLower(string(index))
	for _, domain := range []string{"fonts.googleapis.com", "fonts.gstatic.com"} {
		if strings.Contains(lower, domain) {
			t.Fatalf("构建入口仍依赖外部字体: %s", domain)
		}
	}
	doc, err := html.Parse(bytes.NewReader(index))
	if err != nil {
		t.Fatal(err)
	}
	scripts := 0
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			src := ""
			for _, attr := range node.Attr {
				if strings.HasPrefix(attr.Key, "on") {
					t.Errorf("入口含内联事件处理器 %s", attr.Key)
				}
				if attr.Key == "src" {
					src = attr.Val
				}
			}
			if node.Data == "script" {
				scripts++
				if !strings.HasPrefix(src, "/assets/") || strings.HasPrefix(src, "//") {
					t.Errorf("脚本必须来自同源构建资源，得到 %q", src)
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					if strings.TrimSpace(child.Data) != "" {
						t.Error("入口含内联脚本内容")
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	if scripts == 0 {
		t.Fatal("真实构建入口没有模块脚本，不能验证 CSP 兼容性")
	}
}

func TestDefaultCSPOnRegisteredProductionResponses(t *testing.T) {
	t.Setenv("CSP_MODE", "")
	cfg := config.Load()
	if cfg.CSPMode != "report-only" {
		t.Fatalf("default=%q", cfg.CSPMode)
	}
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Close() })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler.Register(router, "http://localhost", false)
	web.Register(router, cfg.CSPMode)
	index, err := os.ReadFile("dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/index.html", "/node", "/healthz", "/api/v1/missing", "/api/v1/sub/render/test", "/system-info", "/assets/missing.js"} {
		out := httptest.NewRecorder()
		router.ServeHTTP(out, httptest.NewRequest(http.MethodGet, path, nil))
		isHTML := path == "/" || path == "/index.html" || path == "/node"
		if got := out.Header().Get("Content-Security-Policy-Report-Only"); (got != "") != isHTML {
			t.Errorf("%s report-only=%q", path, got)
		}
		if got := out.Header().Get("Content-Security-Policy"); got != "" {
			t.Errorf("%s unexpectedly enforced: %s", path, got)
		}
		if isHTML && (out.Code != http.StatusOK || !bytes.Equal(out.Body.Bytes(), index)) {
			t.Errorf("%s did not return the actual built index: %d", path, out.Code)
		}
		if out.Header().Get("X-Content-Type-Options") != "nosniff" || out.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s lost baseline security headers", path)
		}
	}
	entries, err := os.ReadDir("dist/assets")
	if err != nil {
		t.Fatal(err)
	}
	js := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			js++
			out := httptest.NewRecorder()
			router.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/assets/"+entry.Name(), nil))
			if out.Code != http.StatusOK || out.Header().Get("Content-Security-Policy") != "" || out.Header().Get("Content-Security-Policy-Report-Only") != "" {
				t.Fatalf("asset %s response=%d headers=%s", entry.Name(), out.Code, out.Header())
			}
		}
	}
	if js == 0 {
		t.Fatal("真实构建没有 JS 资源")
	}
}
