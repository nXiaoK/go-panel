package state

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// sensitiveLine 匹配可能携带凭据的日志行；匹配后从关键字之后整行替换，宁可多删也不泄露。
var sensitiveLine = regexp.MustCompile(`(?i)(password|passwd|secret|token|jwt|authorization|api[_-]?key|key=|密码|密钥|令牌)`)

const redactedMark = "[已脱敏]"

// Redact 对子进程输出逐行脱敏，并只保留最后 limit 字节（错误原因通常在末尾），不截断 UTF-8 字符。
func Redact(s string, limit int) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if loc := sensitiveLine.FindStringIndex(line); loc != nil {
			lines[i] = line[:loc[1]] + " " + redactedMark
		}
	}
	return TruncateTail(strings.Join(lines, "\n"), limit)
}

// TruncateTail 只保留字符串最后 limit 字节，截断时以 “…” 开头；limit 不大于 0 时返回空串。
func TruncateTail(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	const ellipsis = "…"
	budget := limit - len(ellipsis)
	if budget <= 0 {
		return ""
	}
	start := len(s) - budget
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return ellipsis + s[start:]
}
