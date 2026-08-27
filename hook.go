package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// runHook implements the PreToolUse confinement hook for the assess agent:
// deny any Read outside the soundings fetch output directory. The assess
// agent reads externally-authored content, so an injection inside a diff
// could otherwise steer it to read secrets elsewhere on disk (~/.ssh,
// .env files) and leak them into the analysis it returns. The agent's
// frontmatter already limits it to the Read tool; this hook limits where
// Read may point. For every other caller the hook emits nothing ("no
// opinion"), leaving the session's normal permission flow untouched.
func runHook(stdin io.Reader, stdout io.Writer) error {
	var in struct {
		ToolName  string `json:"tool_name"`
		AgentType string `json:"agent_type"` // set only for subagent calls; "soundings:assess" when installed as a plugin
		ToolInput struct {
			FilePath string `json:"file_path"`
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return fmt.Errorf("parsing hook input: %w", err)
	}
	assess := in.AgentType == "assess" || strings.HasSuffix(in.AgentType, ":assess")
	if !assess || in.ToolName != "Read" {
		return nil
	}
	// Allowed: a path component starting with "soundings-" (the prefix of
	// every fetch output directory) and no ".." path segment - the index
	// hands the agent absolute paths, so traversal is never legitimate.
	// Checked segment-by-segment (not via strings.Contains) because slugified
	// directory names can legitimately contain a literal ".." substring, e.g.
	// GitHub compare-range slugs like "sha1...sha2".
	path := filepath.ToSlash(in.ToolInput.FilePath)
	if strings.Contains("/"+path, "/soundings-") && !hasDotDotSegment(path) {
		return nil
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":      "PreToolUse",
			"permissionDecision": "deny",
			"permissionDecisionReason": fmt.Sprintf(
				"soundings: the assess agent may only read the fetched soundings-* data directory; %q is outside it", path),
		},
	})
}

// hasDotDotSegment reports whether path contains a ".." path segment, as
// opposed to a ".." substring inside a longer segment (e.g. "sha1...sha2").
func hasDotDotSegment(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}
