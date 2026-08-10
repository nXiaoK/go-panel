package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nXiaoK/go-panel/internal/crypto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
)

type trackingReadCloser struct {
	reader io.Reader
	err    error
	closed bool
}

type countingInfiniteReadCloser struct {
	read   int
	closed bool
}

func (b *countingInfiniteReadCloser) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	b.read += len(p)
	return len(p), nil
}

func (b *countingInfiniteReadCloser) Close() error {
	b.closed = true
	return nil
}

func (b *trackingReadCloser) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	return b.reader.Read(p)
}

func (b *trackingReadCloser) Close() error {
	b.closed = true
	return nil
}

func TestLimitBodyRejectsOversizeChunkedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(LimitBody(16))
	handlerCalls := 0
	r.POST("/", func(c *gin.Context) {
		handlerCalls++
		_, err := io.ReadAll(c.Request.Body)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 17)))
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	if handlerCalls != 0 {
		t.Fatalf("oversized chunked body invoked handler %d times", handlerCalls)
	}
}

func TestLimitBodyRejectsKnownOversizeBeforeHandlerAndClosesBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(LimitBody(16))
	handlerCalls := 0
	r.POST("/", func(c *gin.Context) {
		handlerCalls++
		c.Status(http.StatusNoContent)
	})
	body := &trackingReadCloser{reader: strings.NewReader(strings.Repeat("x", 17))}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = body
	req.ContentLength = 17
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge || handlerCalls != 0 {
		t.Fatalf("status=%d handlerCalls=%d, want 413 and zero calls", w.Code, handlerCalls)
	}
	if !body.closed {
		t.Fatal("original oversized body was not closed")
	}
}

func TestLimitBodyReadsAtMostMaxPlusOne(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(LimitBody(16))
	handlerCalls := 0
	r.POST("/", func(c *gin.Context) {
		handlerCalls++
		c.Status(http.StatusNoContent)
	})
	body := &countingInfiniteReadCloser{}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = body
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge || handlerCalls != 0 {
		t.Fatalf("status=%d handlerCalls=%d, want 413 and zero calls", w.Code, handlerCalls)
	}
	if body.read != 17 {
		t.Fatalf("body bytes read=%d, want max+1=17", body.read)
	}
	if !body.closed {
		t.Fatal("bounded infinite body was not closed")
	}
}

func TestLimitBodyAllowsExactLimitAndRebuildsBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(LimitBody(16))
	wantBody := "1234567890abcdef"
	handlerCalls := 0
	r.POST("/", func(c *gin.Context) {
		handlerCalls++
		if c.Request.ContentLength != int64(len(wantBody)) {
			t.Fatalf("ContentLength=%d, want %d", c.Request.ContentLength, len(wantBody))
		}
		got, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Fatalf("read rebuilt body: %v", err)
		}
		if string(got) != wantBody {
			t.Fatalf("rebuilt body=%q, want %q", got, wantBody)
		}
		c.Status(http.StatusNoContent)
	})
	body := &trackingReadCloser{reader: strings.NewReader(wantBody)}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = body
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent || handlerCalls != 1 {
		t.Fatalf("status=%d handlerCalls=%d", w.Code, handlerCalls)
	}
	if !body.closed {
		t.Fatal("original accepted body was not closed")
	}
}

func TestLimitBodyReadErrorReturnsBadRequestWithoutHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var observedErr error
	r.Use(func(c *gin.Context) {
		c.Next()
		if last := c.Errors.Last(); last != nil {
			observedErr = last.Err
		}
	})
	r.Use(LimitBody(16))
	handlerCalls := 0
	r.POST("/", func(c *gin.Context) {
		handlerCalls++
		c.Status(http.StatusNoContent)
	})
	readErr := errors.New("injected body read failure")
	body := &trackingReadCloser{err: readErr}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = body
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || handlerCalls != 0 {
		t.Fatalf("status=%d handlerCalls=%d, want 400 and zero calls", w.Code, handlerCalls)
	}
	if !body.closed {
		t.Fatal("body with read error was not closed")
	}
	if !errors.Is(observedErr, readErr) {
		t.Fatalf("observed error=%v, want %v", observedErr, readErr)
	}
}

func TestBackupRestoreKeepsExplicitLargeBodyLimit(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	if err := model.DB.Model(&model.User{}).Where("id = ?", 1).Update("must_change_pwd", 0).Error; err != nil {
		t.Fatalf("unlock admin: %v", err)
	}
	crypto.InitJwt("backup-limit-test-secret")
	token, err := crypto.GenerateToken(1, "admin_user", 0, 0)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "large-invalid.db")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), 2<<20)); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	Register(r, "", false)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backup/restore", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want backup handler response", w.Code, w.Body.String())
	}
	var response result.R
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(response.Msg, "不是有效的 SQLite 数据库文件") {
		t.Fatalf("msg=%q, want proof multipart body reached backup validation", response.Msg)
	}
}

func TestJSONAPILimitRejectsOversizeChunkedBody(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	Register(r, "", false)

	body := `{"username":"` + strings.Repeat("x", (1<<20)+1) + `","password":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s, want %d", w.Code, w.Body.String(), http.StatusRequestEntityTooLarge)
	}
}

func TestJSONAPILimitRejectsShortJSONWithOversizeTrailingWhitespace(t *testing.T) {
	if err := model.Init(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer model.Close()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	Register(r, "", false)

	body := `{"username":"missing","password":"x"}` + strings.Repeat(" ", (1<<20)+1)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s, want %d", w.Code, w.Body.String(), http.StatusRequestEntityTooLarge)
	}
}
