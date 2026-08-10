package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAWSR2ObjectStoreUsesSignedPathStyleS3Requests(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		seen[request.Method]++
		mu.Unlock()
		if !strings.Contains(request.Header.Get("Authorization"), "Credential=test-access/") ||
			!strings.Contains(request.Header.Get("Authorization"), "/auto/s3/aws4_request") {
			t.Errorf("request is not signed for R2: %s", request.Header.Get("Authorization"))
			http.Error(w, "missing signature", http.StatusForbidden)
			return
		}

		switch request.Method {
		case http.MethodHead:
			if request.URL.Path != "/test-bucket" {
				t.Errorf("HEAD path=%q", request.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			if request.URL.Path != "/test-bucket/prefix/backup.db" {
				t.Errorf("PUT path=%q", request.URL.Path)
			}
			if request.Header.Get("X-Amz-Meta-Sha256") != strings.Repeat("a", 64) {
				t.Errorf("PUT sha256 metadata=%q", request.Header.Get("X-Amz-Meta-Sha256"))
			}
			body, err := io.ReadAll(request.Body)
			if err != nil || string(body) != "sqlite-backup" {
				t.Errorf("PUT body=%q err=%v", body, err)
			}
			w.Header().Set("ETag", `"etag"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			if request.URL.Path != "/test-bucket" || request.URL.Query().Get("list-type") != "2" || request.URL.Query().Get("prefix") != "prefix/" {
				t.Errorf("LIST request path=%q query=%q", request.URL.Path, request.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>test-bucket</Name><Prefix>prefix/</Prefix><KeyCount>1</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>
  <Contents><Key>prefix/backup.db</Key><LastModified>2026-08-02T03:00:00.000Z</LastModified><ETag>&quot;etag&quot;</ETag><Size>13</Size><StorageClass>STANDARD</StorageClass></Contents>
</ListBucketResult>`)
		case http.MethodDelete:
			if request.URL.Path != "/test-bucket/prefix/backup.db" {
				t.Errorf("DELETE path=%q", request.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	settings := r2ResolvedSettings{
		AccountID:       "0123456789abcdef0123456789abcdef",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
	}
	store, err := newAWSR2ObjectStoreAt(settings, server.URL, server.Client())
	if err != nil {
		t.Fatalf("create R2 client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.HeadBucket(ctx, "test-bucket"); err != nil {
		t.Fatalf("head bucket: %v", err)
	}

	path := filepath.Join(t.TempDir(), "backup.db")
	if err := os.WriteFile(path, []byte("sqlite-backup"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := store.PutFile(ctx, "test-bucket", "prefix/backup.db", path, strings.Repeat("a", 64), 13); err != nil {
		t.Fatalf("put object: %v", err)
	}
	objects, err := store.ListObjects(ctx, "test-bucket", "prefix/")
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	if len(objects) != 1 || objects[0].Key != "prefix/backup.db" || objects[0].Size != 13 {
		t.Fatalf("objects=%#v", objects)
	}
	if err := store.DeleteObject(ctx, "test-bucket", "prefix/backup.db"); err != nil {
		t.Fatalf("delete object: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, method := range []string{http.MethodHead, http.MethodPut, http.MethodGet, http.MethodDelete} {
		if seen[method] != 1 {
			t.Fatalf("method %s count=%d", method, seen[method])
		}
	}
}
