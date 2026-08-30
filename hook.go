package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
)

// runHook implements the PreToolUse confinement hook for the risk-analyst
// agent: allow Read, Grep, and Glob inside the fetch output directories
// registered by this binary (see registry.go), deny them everywhere else.
// The risk-analyst agent reads externally-authored content, so an injection
// inside a diff could otherwise steer it to read or search secrets elsewhere
// on disk (~/.ssh, .env files) and leak them into the analysis it returns.
// The agent's frontmatter already limits it to Read/Grep/Glob; this hook
// limits where those tools may point.
//
// The explicit "allow" also skips the interactive permission prompt for
// in-bounds reads (the fetch directory lives outside the session's working
// directory, where reads would otherwise prompt), so the isolated stage
// runs without user interaction. Deny and ask rules from the user's
// settings still apply regardless of the allow - the hook can only remove
// the prompt, not override a configured deny.
//
// The hook also pre-approves this plugin's own helper MCP tools (fetch and
// render), the other prompt source of a skill run that plugin-shipped
// configuration can address.
//
// For every other caller the hook emits nothing ("no opinion"), leaving the
// session's normal permission flow untouched.
func runHook(stdin io.Reader, stdout io.Writer) error {
	var in struct {
		ToolName  string `json:"tool_name"`
		AgentType string `json:"agent_type"` // set only for subagent calls; "soundings:risk-analyst" when installed as a plugin
		CWD       string `json:"cwd"`
		ToolInput struct {
			FilePath string `json:"file_path"` // Read
			Path     string `json:"path"`      // Grep, Glob
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return fmt.Errorf("parsing hook input: %w", err)
	}
	// This plugin's own helper MCP tools are pre-approved: the skill's
	// allowed-tools frontmatter constrains what the turn may use but is not
	// a permission grant, so without this the fetch and render calls prompt
	// like any MCP tool - and a plugin cannot ship permission rules. These
	// calls come from the main session (no agent_type), so this check runs
	// before the risk-analyst gate below. User-configured deny and ask
	// rules still apply regardless of the allow.
	if in.ToolName == "mcp__plugin_soundings_helper__fetch" ||
		in.ToolName == "mcp__plugin_soundings_helper__render" {
		return emitDecision(stdout, "allow",
			"soundings: the plugin's own helper MCP tool is pre-approved")
	}
	// Exact match only: a namespaced risk-analyst from any other plugin
	// ("otherplugin:risk-analyst") must not inherit this hook's approvals.
	if in.AgentType != "risk-analyst" && in.AgentType != "soundings:risk-analyst" {
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
	target := resolveForCheck(path, in.CWD)
	for _, dir := range allowedDirs() {
		if underDir(target, dir) {
			return emitDecision(stdout, "allow",
				"soundings: read inside the registered fetch data directory")
		}
	}
	return emitDecision(stdout, "deny", fmt.Sprintf(
		"soundings: the risk-analyst agent may only read the fetch data directory registered for this run; %q is outside it", path))
}

func emitDecision(stdout io.Writer, decision, reason string) error {
	return json.NewEncoder(stdout).Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       decision,
			"permissionDecisionReason": reason,
		},
	})
}

// resolveForCheck turns the tool-call path into the absolute, cleaned,
// symlink-resolved form that registry entries use, so neither a relative
// path, a ".." segment, nor a symlink inside an allowed directory can
// disguise a target outside it. An empty or unresolvable path resolves to
// something outside every registered directory and is therefore denied.
func resolveForCheck(path, cwd string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	return resolveExisting(filepath.Clean(path))
}

// resolveExisting resolves symlinks on the deepest existing ancestor of p
// and reattaches the non-existent remainder, so paths whose final elements
// don't exist yet (a Glob pattern, a not-yet-written file) still resolve
// through any symlinked parents.
func resolveExisting(p string) string {
	rest := ""
	for {
		resolved, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}
