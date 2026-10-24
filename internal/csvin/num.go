package csvin

import (
	"regexp"
	"strconv"
	"strings"
)

// ParseFloat 解析浮点数，空串与非法值返回 (0, false)。
func ParseFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// ParseInt 解析整数，空串与非法值返回 (0, false)。
func ParseInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// 容忍 "3.0" 这种浮点写法。
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return int(v), true
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return v, true
}

// tagSplit 匹配逗号、中文逗号、顿号及其两侧空白。
// Rond 用「英文逗号 + 空格」分隔多值标签（如「童瑶, 园林」）。
var tagSplit = regexp.MustCompile(`\s*[,，、;；]\s*`)

// SplitTags 拆分标签串：去空白、去空段、段内去重（保序）。
func SplitTags(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := tagSplit.Split(s, -1)
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
