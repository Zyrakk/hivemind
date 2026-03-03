package directive

import (
	"regexp"
	"strings"
)

var (
	projectRoutingPrefixRE = regexp.MustCompile(`(?i)^\s*(?:proyecto|project)\s*:\s*(.+)$`)
	directiveLabelPrefixRE = regexp.MustCompile(`(?i)^(?:directriz|directive)\s*:\s*`)
	inlineDirectiveLabelRE = regexp.MustCompile(`(?i)\b(?:directriz|directive)\s*:`)
)

// ParseRouting extracts project routing metadata from free-form directives.
// Supported prefixes:
//   - "Project: <project> ..."
//   - "Proyecto: <project> ..."
func ParseRouting(raw string) (directive string, projectRef string, hasRouting bool) {
	trimmed := strings.TrimSpace(raw)
	matches := projectRoutingPrefixRE.FindStringSubmatch(trimmed)
	if len(matches) != 2 {
		return trimmed, "", false
	}

	rest := strings.TrimSpace(matches[1])
	if rest == "" {
		return "", "", true
	}

	splitAt := len(rest)
	if idx := strings.Index(rest, "."); idx >= 0 && idx < splitAt {
		splitAt = idx
	}
	if idx := strings.IndexAny(rest, "\n\r"); idx >= 0 && idx < splitAt {
		splitAt = idx
	}
	if loc := inlineDirectiveLabelRE.FindStringIndex(rest); loc != nil && loc[0] >= 0 && loc[0] < splitAt {
		splitAt = loc[0]
	}

	projectRef = strings.TrimSpace(rest[:splitAt])
	remainder := ""
	if splitAt < len(rest) {
		remainder = strings.TrimSpace(rest[splitAt:])
	}
	remainder = strings.TrimLeft(remainder, ". \t\r\n")
	remainder = strings.TrimSpace(directiveLabelPrefixRE.ReplaceAllString(remainder, ""))

	return remainder, projectRef, true
}
