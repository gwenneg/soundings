package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// runHook is the PreToolUse confinement hook around the registered fetch
// directories (see registry.go), in both directions: the risk-analyst
// agent may Read/Grep/Glob only inside them (the explicit allow also
// skips the permission prompt, so the isolated stage runs unattended),
// and every other agent - the orchestrating session included - is denied
// those tools wherever they could reach the fetched, externally-authored
// content. It also pre-approves this plugin's own helper MCP tools.
// Everything else gets no opinion; user-configured deny and ask rules
// always override an allow.
func runHook(stdin io.Reader, stdout io.Writer) error {
	var in struct {
		ToolName  string `json:"tool_name"`
		AgentType string `json:"agent_type"` // set only for subagent calls; "soundings:risk-analyst" when installed as a plugin
		CWD       string `json:"cwd"`
		ToolInput struct {
			FilePath string `json:"file_path"` // Read
			Path     string `json:"path"`      // Grep, Glob
			Pattern  string `json:"pattern"`   // Glob; may be absolute, overriding path as the search root
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return fmt.Errorf("parsing hook input: %w", err)
	}
	// Pre-approved because a plugin cannot ship permission rules, and the
	// skill's allowed-tools frontmatter is not a permission grant.
	if in.ToolName == "mcp__plugin_soundings_helper__fetch" ||
		in.ToolName == "mcp__plugin_soundings_helper__render" {
		return emitDecision(stdout, "allow",
			"soundings: the plugin's own helper MCP tool is pre-approved")
	}
	var path string
	switch in.ToolName {
	case "Read":
		path = in.ToolInput.FilePath
	case "Grep", "Glob":
		// Optional; defaults to the session's working directory.
		path = in.ToolInput.Path
	default:
		return nil
	}

	// Exact match only: a namespaced risk-analyst from any other plugin
	// must not inherit this hook's approvals.
	isRiskAnalyst := in.AgentType == "risk-analyst" || in.AgentType == "soundings:risk-analyst"

	authorized, fenced := registryDirs()

	shown := path
	if shown == "" {
		// Grep's pattern is a regex, not a location - show the cwd instead.
		if in.ToolName == "Glob" && in.ToolInput.Pattern != "" {
			shown = in.ToolInput.Pattern
		} else {
			shown = in.CWD
		}
	}

	// No live fetch data anywhere: the outcome is fixed, skip resolution.
	if len(fenced) == 0 && len(authorized) == 0 {
		if isRiskAnalyst {
			return emitDecision(stdout, "deny", fmt.Sprintf(
				"soundings: the risk-analyst agent may only read the fetch data directory registered for this run; %q is outside it", shown))
		}
		return nil
	}

	target := resolvePath(path, in.CWD)

	pattern := in.ToolInput.Pattern
	patternRoot, patternBounded := "", true
	if in.ToolName == "Glob" && pattern != "" {
		patternRoot, patternBounded = globPatternRoot(target, pattern)
	}

	if isRiskAnalyst {
		// Never fold in the allow direction (see underDir).
		var inBounds bool
		switch {
		case !patternBounded:
			inBounds = false
		case patternRoot == "":
			inBounds = insideAny(target, authorized, false)
		case filepath.IsAbs(pattern):
			// The absolute pattern is the effective root; path, when also
			// given, must agree with it.
			inBounds = insideAny(patternRoot, authorized, false) &&
				(path == "" || insideAny(target, authorized, false))
		default:
			inBounds = insideAny(target, authorized, false) && insideAny(patternRoot, authorized, false)
		}
		if inBounds {
			return emitDecision(stdout, "allow",
				"soundings: read inside the registered fetch data directory")
		}
		return emitDecision(stdout, "deny", fmt.Sprintf(
			"soundings: the risk-analyst agent may only read the fetch data directory registered for this run; %q is not confined to it", shown))
	}

	// Keep-out for everyone else. Read touches content only from inside a
	// directory; Grep and Glob search recursively, so a root at or above a
	// fenced directory reaches it too. An unbounded pattern is confined by
	// nothing, so it is denied whenever any fence exists.
	reaches := insideAny(target, fenced, true)
	if in.ToolName != "Read" {
		reaches = reaches || containsAny(target, fenced, true) || !patternBounded ||
			(patternRoot != "" && (insideAny(patternRoot, fenced, true) || containsAny(patternRoot, fenced, true)))
	}
	if reaches {
		return emitDecision(stdout, "deny", fmt.Sprintf(
			"soundings: %q may reach inside a registered fetch data directory holding externally-authored release data; only the isolated risk-analyst stage may read it - inspect the files outside this session if you need to", shown))
	}
	return nil
}

// insideAny reports whether p is one of dirs or inside one; fold as in
// underDir (true only for deny-direction checks).
func insideAny(p string, dirs []string, fold bool) bool {
	for _, dir := range dirs {
		if underDir(p, dir, fold) {
			return true
		}
	}
	return false
}

// containsAny reports whether any of dirs lies under p.
func containsAny(p string, dirs []string, fold bool) bool {
	for _, dir := range dirs {
		if underDir(dir, p, fold) {
			return true
		}
	}
	return false
}

// globPatternRoot resolves the deepest directory a glob pattern is
// anchored to: its static prefix, anchored at base when relative. The raw
// pattern is parsed, never pre-Cleaned - Clean would cancel a ".." against
// a literal "**" segment and hide an escape. The second result is false
// when no static prefix can bound the pattern's reach: a brace group that
// may expand across separators (a "/" after it, e.g. "{/etc,x}/**"), or a
// ".." at or after the first wildcard (a zero-match "**" lets it climb
// out). A separator-free group like "*.{ts,tsx}" stays bounded - it
// expands to filenames, not paths.
func globPatternRoot(base, pattern string) (string, bool) {
	if i := strings.Index(pattern, "{"); i >= 0 {
		if strings.Contains(pattern[i:], "/") || strings.Contains(pattern, "..") {
			return "", false
		}
	}
	prefix := ""
	if strings.HasPrefix(pattern, "/") {
		prefix = string(filepath.Separator)
	}
	seenWildcard := false
	for _, seg := range strings.Split(pattern, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if strings.ContainsAny(seg, `*?[{`) {
			seenWildcard = true
			continue
		}
		if seg == ".." && seenWildcard {
			return "", false
		}
		if !seenWildcard {
			prefix = filepath.Join(prefix, seg)
		}
	}
	if prefix == "" {
		prefix = "."
	}
	return resolvePath(prefix, base), true
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

// resolvePath is the one normalization every path compared against
// registry entries must share - absolute against base, cleaned, symlinks
// resolved on existing ancestors - so a relative path, a ".." segment, or
// a symlink cannot disguise a target's real location.
func resolvePath(p, base string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return resolveExisting(filepath.Clean(p))
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
