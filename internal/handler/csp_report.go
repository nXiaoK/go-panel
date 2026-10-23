package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
)

const (
	// 单个报告通常不足 2 KB；16 KB 足够容纳 Reporting API 的批量数组，同时限制匿名请求的内存占用。
	cspReportMaxBody = 16 << 10
	// 每个报告最多记录 5 条违规，避免一次批量请求刷屏。
	cspReportMaxEntries = 5
	// 每个来源 IP 每分钟最多记录 20 次、全站每分钟最多 200 次；超出时丢弃但返回 204，浏览器不会重试。
	cspReportPerClientLimit = 20
	cspReportGlobalLimit    = 200
	cspReportWindow         = time.Minute
	// 日志中每个字段最多保留 200 个字符；URL 另行缩减为来源，避免令牌或页面内容落盘。
	cspReportMaxField = 200
)

// cspReportLimiter 是固定窗口计数器；端点公开且无鉴权，只用于限制日志量，不承担安全判定。
type cspReportLimiter struct {
	mu          sync.Mutex
	now         func() time.Time
	windowStart time.Time
	global      int
	clients     map[string]int
}

func newCSPReportLimiter(now func() time.Time) *cspReportLimiter {
	return &cspReportLimiter{now: now, clients: make(map[string]int)}
}

func (l *cspReportLimiter) allow(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	current := l.now()
	if current.Sub(l.windowStart) >= cspReportWindow {
		l.windowStart, l.global = current, 0
		clear(l.clients)
	}
	if l.global >= cspReportGlobalLimit || l.clients[client] >= cspReportPerClientLimit {
		return false
	}
	// 每次新增来源都会消耗全站额度，因此每窗口最多保留 cspReportGlobalLimit 个来源。
	l.global++
	l.clients[client]++
	return true
}

var defaultCSPReportLimiter = newCSPReportLimiter(time.Now)

// cspViolation 只保留排查所需字段；兼容 report-uri（application/csp-report）与 Reporting API（application/reports+json）。
type cspViolation struct {
	Directive   string
	BlockedURL  string
	DocumentURL string
	Disposition string
}

func parseCSPReport(body []byte) []cspViolation {
	var legacy struct {
		Report *struct {
			DocumentURI        string `json:"document-uri"`
			ViolatedDirective  string `json:"violated-directive"`
			EffectiveDirective string `json:"effective-directive"`
			BlockedURI         string `json:"blocked-uri"`
			Disposition        string `json:"disposition"`
		} `json:"csp-report"`
	}
	if err := json.Unmarshal(body, &legacy); err == nil && legacy.Report != nil {
		directive := legacy.Report.EffectiveDirective
		if directive == "" {
			directive = legacy.Report.ViolatedDirective
		}
		return []cspViolation{{directive, legacy.Report.BlockedURI, legacy.Report.DocumentURI, legacy.Report.Disposition}}
	}
	var reports []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
		Body struct {
			DocumentURL        string `json:"documentURL"`
			EffectiveDirective string `json:"effectiveDirective"`
			BlockedURL         string `json:"blockedURL"`
			Disposition        string `json:"disposition"`
		} `json:"body"`
	}
	if err := json.Unmarshal(body, &reports); err != nil {
		return nil
	}
	violations := make([]cspViolation, 0, min(len(reports), cspReportMaxEntries))
	for _, report := range reports {
		if report.Type != "csp-violation" {
			continue
		}
		document := report.Body.DocumentURL
		if document == "" {
			document = report.URL
		}
		violations = append(violations, cspViolation{report.Body.EffectiveDirective, report.Body.BlockedURL, document, report.Body.Disposition})
		if len(violations) == cspReportMaxEntries {
			break
		}
	}
	return violations
}

// sanitizeCSPField 只记录指令或处置类型的首个标记，不记录 violated-directive 可能附带的来源列表。
// 移除控制字符并截断，避免匿名报告注入多行日志或无限扩张单条日志。
func sanitizeCSPField(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	value = fields[0]
	// CSP 指令和 disposition 均为 ASCII 字母及连字符；其他内容不能作为标签写入日志。
	if strings.ContainsFunc(value, func(r rune) bool { return (r < 'a' || r > 'z') && r != '-' }) {
		return "[invalid]"
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	if runes := []rune(value); len(runes) > cspReportMaxField {
		value = string(runes[:cspReportMaxField]) + "…"
	}
	return value
}

// sanitizeCSPURL 仅保留 HTTP(S)/WS(S) 来源（协议、主机、端口），连路径也不记录，
// 因为订阅令牌可能直接嵌在路径中；data/blob 等只记录协议，非法或相对 URL 不回显原文。
func sanitizeCSPURL(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "", "inline", "eval", "wasm-eval", "trusted-types-sink", "trusted-types-policy":
		return value
	}
	parsed, err := url.Parse(value)
	if err != nil || strings.ContainsFunc(value, unicode.IsControl) {
		return "[invalid URL]"
	}
	if !parsed.IsAbs() {
		return "[relative URL]"
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "ws" && scheme != "wss" {
		if len(scheme) > cspReportMaxField {
			return "[invalid URL]"
		}
		return scheme + ":"
	}
	if parsed.Host == "" {
		return "[invalid URL]"
	}
	origin := (&url.URL{Scheme: scheme, Host: parsed.Host}).String()
	if len([]rune(origin)) > cspReportMaxField {
		return "[invalid URL]"
	}
	return origin
}

// cspReport 接收浏览器 CSP 违规报告并写入日志；始终返回 204，不向匿名调用方暴露解析或限流状态。
func cspReport(c *gin.Context) {
	handleCSPReport(c, defaultCSPReportLimiter)
}

func handleCSPReport(c *gin.Context, limiter *cspReportLimiter) {
	defer c.Status(http.StatusNoContent)
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, cspReportMaxBody))
	if err != nil || len(body) == 0 || !limiter.allow(c.ClientIP()) {
		return
	}
	for _, violation := range parseCSPReport(body) {
		log.Printf("CSP 违规报告：directive=%q blocked=%q document=%q disposition=%q",
			sanitizeCSPField(violation.Directive), sanitizeCSPURL(violation.BlockedURL),
			sanitizeCSPURL(violation.DocumentURL), sanitizeCSPField(violation.Disposition))
	}
}
