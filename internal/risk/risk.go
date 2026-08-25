// Package risk classifies changed files into risk tiers based on filename
// patterns, so the analyzing agent can prioritize what to read in full.
// The tiers are a prioritization hint, not a verdict: content (commit
// messages, the patches themselves) can and should override them.
package risk

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
)

// FileRiskLevel represents the risk level of a file
type FileRiskLevel int

const (
	RiskCritical FileRiskLevel = iota
	RiskHigh
	RiskMedium
	RiskLow
)

// Embedded risk patterns JSON file
//
//go:embed risk_patterns.json
var riskPatternsJSON []byte

// File risk classification patterns - loaded from JSON file at initialization
var riskPatterns map[FileRiskLevel][]string

// init loads the risk patterns from the embedded JSON file
func init() {
	// Parse the JSON structure
	var patterns struct {
		Critical []string `json:"critical"`
		High     []string `json:"high"`
		Medium   []string `json:"medium"`
		Low      []string `json:"low"`
	}

	if err := json.Unmarshal(riskPatternsJSON, &patterns); err != nil {
		// Panic on JSON parse error since the file is embedded at compile time
		// This indicates a programming error that should be caught during development
		panic(fmt.Sprintf("Failed to parse embedded risk_patterns.json: %v", err))
	}

	// Map JSON fields to FileRiskLevel enum
	riskPatterns = map[FileRiskLevel][]string{
		RiskCritical: patterns.Critical,
		RiskHigh:     patterns.High,
		RiskMedium:   patterns.Medium,
		RiskLow:      patterns.Low,
	}
}

// ClassifyFile returns the risk tier of a file as a string
// ("critical", "high", "medium", "low") for use in the fetch index.
func ClassifyFile(filename string) string {
	switch classifyFileRisk(filename) {
	case RiskCritical:
		return "critical"
	case RiskHigh:
		return "high"
	case RiskLow:
		return "low"
	default:
		return "medium"
	}
}

// classifyFileRisk determines the risk level of a file based on its filename
func classifyFileRisk(filename string) FileRiskLevel {
	lower := strings.ToLower(filename)

	// Check patterns in order of risk level (highest to lowest)
	for _, risk := range []FileRiskLevel{RiskCritical, RiskHigh, RiskMedium, RiskLow} {
		if matchesAnyPattern(lower, riskPatterns[risk]) {
			return risk
		}
	}

	// Default to medium risk if no pattern matches
	return RiskMedium
}

// matchesAnyPattern checks if the filename matches any of the given glob patterns
func matchesAnyPattern(filename string, patterns []string) bool {
	// Split filename once before looping through patterns for efficiency
	parts := strings.Split(filename, "/")

	for _, pattern := range patterns {
		// Try glob pattern matching on the full filename first
		matched, err := filepath.Match(pattern, filename)
		if err != nil {
			slog.Warn("Invalid glob pattern", "pattern", pattern, "error", err)
			continue
		}
		if matched {
			return true
		}

		if strings.Contains(pattern, "/") {
			// filepath.Match's * never crosses '/', so a slash pattern like
			// "*/api/*" only matches paths with exactly its component count.
			// Slide the pattern over every same-length window of the path so
			// "backend/src/api/routes.py" still matches "*/api/*".
			patParts := strings.Split(pattern, "/")
			if matchesComponentWindow(parts, patParts) {
				return true
			}
			continue
		}

		// Slashless pattern: check every path component
		// This handles patterns like "auth*", "*migration*", etc.
		for _, part := range parts {
			matched, err := filepath.Match(pattern, part)
			if err != nil {
				// Already warned about this pattern above
				break
			}
			if matched {
				return true
			}
		}
	}
	return false
}

// matchesComponentWindow reports whether any contiguous window of path
// components matches the pattern's components one-to-one.
func matchesComponentWindow(parts, patParts []string) bool {
	if len(patParts) > len(parts) {
		return false
	}
	for start := 0; start+len(patParts) <= len(parts); start++ {
		all := true
		for i, pp := range patParts {
			ok, err := filepath.Match(pp, parts[start+i])
			if err != nil || !ok {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}
