package state

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRedactMasksCredentialLines(t *testing.T) {
	input := strings.Join([]string{
		"2026/11/01 go-panel v0.3.0 启动",
		"generated administrator credentials: username=admin_user password=S3cret!",
		"JWT_SECRET=0123456789abcdef",
		"Authorization: Bearer abc.def",
		"管理员初始密码：Hunter2",
		"数据库初始化失败: 自动迁移失败",
	}, "\n")
	got := Redact(input, 4096)
	for _, leaked := range []string{"S3cret!", "0123456789abcdef", "abc.def", "Hunter2"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaks %q:\n%s", leaked, got)
		}
	}
	for _, kept := range []string{"启动", "自动迁移失败", "username=admin_user password"} {
		if !strings.Contains(got, kept) {
			t.Fatalf("redacted output lost %q:\n%s", kept, got)
		}
	}
}

func TestTruncateTailKeepsEndAndUTF8(t *testing.T) {
	input := strings.Repeat("迁移失败", 100) + "最后一行"
	got := TruncateTail(input, 64)
	if len(got) > 64 || !utf8.ValidString(got) || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "最后一行") {
		t.Fatalf("TruncateTail=%q (len %d)", got, len(got))
	}
	if TruncateTail("short", 64) != "short" || TruncateTail("abc", 0) != "" {
		t.Fatal("short inputs and zero limits must be handled")
	}
}
