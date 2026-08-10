package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

const handlerTestAdminPassword = "handler test admin password"

func initHandlerTestDB(t *testing.T) {
	t.Helper()
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db"), model.BootstrapOptions{
		AdminPassword: handlerTestAdminPassword,
	}); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = model.Close()
	})
}

func newHandlerTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r, "", false)
	return r
}

func performJSONRequest(r http.Handler, method, target, remoteAddr, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeResult(t *testing.T, w *httptest.ResponseRecorder) result.R {
	t.Helper()
	var response result.R
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return response
}

func TestSubStoreGETIsMethodNotAllowedWithoutReadingQueryCredentials(t *testing.T) {
	initHandlerTestDB(t)
	r := newHandlerTestRouter()

	w := performJSONRequest(
		r,
		http.MethodGet,
		"/api/v1/open_api/sub_store?user=admin_user&pwd="+url.QueryEscape(handlerTestAdminPassword),
		"192.0.2.220:1234",
		"",
	)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d, want 405; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow=%q, want POST", got)
	}
	response := decodeResult(t, w)
	if response.Msg != "请使用 POST 请求或订阅 token" {
		t.Fatalf("msg=%q, want method guidance", response.Msg)
	}
}

func TestSubStorePOSTDoesNotUseQueryCredentials(t *testing.T) {
	initHandlerTestDB(t)
	r := newHandlerTestRouter()

	w := performJSONRequest(
		r,
		http.MethodPost,
		"/api/v1/open_api/sub_store?user=admin_user&pwd="+url.QueryEscape(handlerTestAdminPassword),
		"192.0.2.221:1234",
		`{"tunnel":"-1"}`,
	)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
	if got := w.Header().Get("subscription-userinfo"); got != "" {
		t.Fatalf("query credentials authenticated POST, subscription-userinfo=%q", got)
	}
	response := decodeResult(t, w)
	if response.Code == 0 {
		t.Fatalf("query credentials authenticated POST: %#v", response)
	}
}

func TestLoginAndSubStoreShareCredentialAttempts(t *testing.T) {
	initHandlerTestDB(t)
	r := newHandlerTestRouter()
	const remoteAddr = "192.0.2.222:1234"

	for i := 0; i < 4; i++ {
		w := performJSONRequest(
			r,
			http.MethodPost,
			"/api/v1/user/login",
			remoteAddr,
			`{"username":"admin_user","password":"wrong password"}`,
		)
		response := decodeResult(t, w)
		if response.Code == 0 {
			t.Fatalf("login failure %d succeeded: %#v", i+1, response)
		}
	}

	w := performJSONRequest(
		r,
		http.MethodPost,
		"/api/v1/open_api/sub_store",
		remoteAddr,
		`{"user":"admin_user","pwd":"wrong password","tunnel":"-1"}`,
	)
	if response := decodeResult(t, w); response.Code == 0 {
		t.Fatalf("subscription failure succeeded: %#v", response)
	}

	w = performJSONRequest(
		r,
		http.MethodPost,
		"/api/v1/user/login",
		remoteAddr,
		`{"username":"admin_user","password":"handler test admin password"}`,
	)
	response := decodeResult(t, w)
	if response.Code == 0 || response.Msg != "登录尝试过多，请稍后重试" {
		t.Fatalf("sixth shared attempt=%#v, want attempt limited", response)
	}

	var user model.User
	if err := model.DB.First(&user, 1).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.LoginFailCount != 5 {
		t.Fatalf("persistent failure count=%d, want 5 shared failures", user.LoginFailCount)
	}
}

func TestLoginAndSubStoreShareAtomicConcurrentCapacity(t *testing.T) {
	initHandlerTestDB(t)
	r := newHandlerTestRouter()
	const remoteAddr = "192.0.2.223:1234"

	for i := 0; i < 4; i++ {
		w := performJSONRequest(
			r,
			http.MethodPost,
			"/api/v1/user/login",
			remoteAddr,
			`{"username":"admin_user","password":"wrong password"}`,
		)
		if response := decodeResult(t, w); response.Code == 0 {
			t.Fatalf("seed failure %d succeeded: %#v", i+1, response)
		}
	}

	const contenders = 12
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				responses <- performJSONRequest(
					r,
					http.MethodPost,
					"/api/v1/user/login",
					remoteAddr,
					`{"username":"admin_user","password":"wrong password"}`,
				)
				return
			}
			responses <- performJSONRequest(
				r,
				http.MethodPost,
				"/api/v1/open_api/sub_store",
				remoteAddr,
				`{"user":"admin_user","pwd":"wrong password","tunnel":"-1"}`,
			)
		}(i)
	}
	close(start)
	wg.Wait()
	close(responses)

	limited := 0
	for w := range responses {
		response := decodeResult(t, w)
		if response.Code == 0 {
			t.Fatalf("concurrent credential failure succeeded: %#v", response)
		}
		if response.Msg == "登录尝试过多，请稍后重试" {
			limited++
		}
	}
	if limited != contenders-1 {
		t.Fatalf("limited responses=%d, want %d; only the fifth failure may enter verification", limited, contenders-1)
	}

	var user model.User
	if err := model.DB.First(&user, 1).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if user.LoginFailCount != 5 {
		t.Fatalf("persistent failure count=%d, want exactly 5", user.LoginFailCount)
	}
}
