package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// runHook implements the PreToolUse confinement hook for the risk-analyst
// agent: deny any Read, Grep, or Glob outside the soundings fetch output
// directory. The risk-analyst agent reads externally-authored content, so
// an injection inside a diff could otherwise steer it to read or search
// secrets elsewhere on disk (~/.ssh, .env files) and leak them into the
// analysis it returns. The agent's frontmatter already limits it to
// Read/Grep/Glob; this hook limits where those tools may point. For every
// other caller the hook emits nothing ("no opinion"), leaving the
// session's normal permission flow untouched.
func runHook(stdin io.Reader, stdout io.Writer) error {
	var in struct {
		ToolName  string `json:"tool_name"`
		AgentType string `json:"agent_type"` // set only for subagent calls; "soundings:risk-analyst" when installed as a plugin
		ToolInput struct {
			FilePath string `json:"file_path"` // Read
			Path     string `json:"path"`      // Grep, Glob
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return fmt.Errorf("parsing hook input: %w", err)
	}
	riskAnalyst := in.AgentType == "risk-analyst" || strings.HasSuffix(in.AgentType, ":risk-analyst")
	if !riskAnalyst {
		return nil
	}
	var path string
	switch in.ToolName {
	case "Read":
		path = in.ToolInput.FilePath
	case "Grep", "Glob":
		// Path is optional for these tools and defaults to the session's
		// working directory when omitted - that default is never the fetch
		// output directory, so an empty path is denied same as any other
		// path outside it.
		path = in.ToolInput.Path
	default:
		return nil
	}
	// Allowed: a path component starting with "soundings-" (the prefix of
	// every fetch output directory) and no ".." path segment - the index
	// hands the agent absolute paths, so traversal is never legitimate.
	// Checked segment-by-segment (not via strings.Contains) because slugified
	// directory names can legitimately contain a literal ".." substring, e.g.
	// GitHub compare-range slugs like "sha1...sha2".
	path = filepath.ToSlash(path)
	if strings.Contains("/"+path, "/soundings-") && !hasDotDotSegment(path) {
		return nil
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":      "PreToolUse",
			"permissionDecision": "deny",
			"permissionDecisionReason": fmt.Sprintf(
				"soundings: the risk-analyst agent may only read the fetched soundings-* data directory; %q is outside it", path),
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
