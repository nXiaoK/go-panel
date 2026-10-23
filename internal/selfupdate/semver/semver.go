// Package semver 提供面板更新使用的稳定语义版本解析与比较。
//
// 只接受 vX.Y.Z（可带 +build 元数据）；预发布（-rc 等）、edge-*、build-* 与 dev
// 构建一律视为非稳定版本，不参与跨版本更新判断。
package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version 是解析后的稳定版本号：主版本、次版本、补丁版本。
type Version [3]uint64

// Normalize 为缺少 v 前缀的标签补齐前缀；空值原样返回。
func Normalize(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "v") {
		return raw
	}
	return "v" + raw
}

// Parse 解析稳定版本；带预发布后缀、段数不为 3 或包含非数字时返回 false。
func Parse(raw string) (Version, bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if raw == "" || strings.Contains(raw, "-") {
		return Version{}, false
	}
	if index := strings.IndexByte(raw, '+'); index >= 0 {
		raw = raw[:index]
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	var parsed Version
	for i, part := range parts {
		if part == "" {
			return Version{}, false
		}
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return Version{}, false
		}
		parsed[i] = value
	}
	return parsed, true
}

// Compare 返回 -1、0、1，分别表示 a 低于、等于、高于 b。
func Compare(a, b Version) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// String 返回带 v 前缀的规范形式，例如 v1.2.3。
func (v Version) String() string {
	return fmt.Sprintf("v%d.%d.%d", v[0], v[1], v[2])
}

// Less 判断稳定版本 a 是否低于 b；任一参数不是稳定版本时返回错误。
func Less(a, b string) (bool, error) {
	left, ok := Parse(a)
	if !ok {
		return false, fmt.Errorf("%q 不是稳定语义版本", a)
	}
	right, ok := Parse(b)
	if !ok {
		return false, fmt.Errorf("%q 不是稳定语义版本", b)
	}
	return Compare(left, right) < 0, nil
}
