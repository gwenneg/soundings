package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hookSetup points the registry at a per-test cache dir and registers one
// fetch data directory, returning that directory's path.
func hookSetup(t *testing.T) string {
	t.Helper()
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dataDir := filepath.Join(t.TempDir(), "soundings-abc123")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := registerDir(dataDir); err != nil {
		t.Fatalf("registerDir: %v", err)
	}
	return dataDir
}

func invokeHook(t *testing.T, agentType, tool, path string) string {
	t.Helper()
	return invokeHookFull(t, agentType, tool, path, "", "/")
}

func invokeHookFull(t *testing.T, agentType, tool, path, pattern, cwd string) string {
	t.Helper()
	in := fmt.Sprintf(`{"tool_name":%q,"agent_type":%q,"cwd":%q,"tool_input":{"file_path":%q,"path":%q,"pattern":%q}}`,
		tool, agentType, cwd, path, path, pattern)
	var out strings.Builder
	if err := runHook(strings.NewReader(in), &out); err != nil {
		t.Fatalf("runHook: %v", err)
	}
	return out.String()
}

func TestRunHook(t *testing.T) {
	dataDir := hookSetup(t)

	cases := []struct {
		name                  string
		agentType, tool, path string
		want                  string // "allow", "deny", or "" (no opinion)
	}{
		{"risk-analyst allowed on the data dir itself", "risk-analyst", "Read", dataDir, "allow"},
		{"risk-analyst allowed on index", "risk-analyst", "Read", filepath.Join(dataDir, "index.json"), "allow"},
		{"risk-analyst allowed on nested patch file", "soundings:risk-analyst", "Read", filepath.Join(dataDir, "patches", "repo", "001-main.go.patch"), "allow"},
		{"risk-analyst allowed Grep in data dir", "soundings:risk-analyst", "Grep", filepath.Join(dataDir, "patches"), "allow"},
		{"risk-analyst allowed Glob in data dir", "soundings:risk-analyst", "Glob", dataDir, "allow"},
		{"risk-analyst denied outside data dir", "risk-analyst", "Read", "/Users/someone/.ssh/id_rsa", "deny"},
		{"risk-analyst denied on empty path", "risk-analyst", "Read", "", "deny"},
		{"risk-analyst denied on traversal out of data dir", "risk-analyst", "Read", filepath.Join(dataDir, "..", "..", "etc", "passwd"), "deny"},
		{"risk-analyst denied on sibling with allowed dir as name prefix", "risk-analyst", "Read", dataDir + "-evil/secret", "deny"},
		{"risk-analyst denied on unregistered soundings-named dir", "risk-analyst", "Read", "/tmp/soundings-unregistered/index.json", "deny"},
		{"risk-analyst denied Grep outside data dir", "risk-analyst", "Grep", "/etc", "deny"},
		{"risk-analyst denied Grep on empty path", "risk-analyst", "Grep", "", "deny"},
		{"risk-analyst denied Glob outside data dir", "risk-analyst", "Glob", "/home/x", "deny"},
		{"helper fetch tool pre-approved for main session", "", "mcp__plugin_soundings_helper__fetch", "", "allow"},
		{"helper render tool pre-approved for main session", "", "mcp__plugin_soundings_helper__render", "", "allow"},
		{"lookalike plugin MCP tool untouched", "", "mcp__plugin_evil_helper__fetch", "", ""},
		{"bare-named helper server untouched", "", "mcp__helper__fetch", "", ""},
		{"main session untouched outside fetch dir", "", "Read", "/Users/someone/.ssh/id_rsa", ""},
		{"main session denied inside fetch dir", "", "Read", filepath.Join(dataDir, "index.json"), "deny"},
		{"main session denied Grep inside fetch dir", "", "Grep", filepath.Join(dataDir, "patches"), "deny"},
		{"main session denied Glob on fetch dir itself", "", "Glob", dataDir, "deny"},
		{"other agents untouched outside fetch dir", "Explore", "Read", "/Users/someone/.ssh/id_rsa", ""},
		{"other agents denied inside fetch dir", "Explore", "Read", filepath.Join(dataDir, "patches", "repo", "001-main.go.patch"), "deny"},
		{"prefix without namespace untouched outside fetch dir", "harisk-analyst", "Read", "/etc/passwd", ""},
		{"other plugin's risk-analyst denied inside fetch dir", "otherplugin:risk-analyst", "Read", dataDir, "deny"},
		{"other plugin's risk-analyst untouched outside fetch dir", "otherplugin:risk-analyst", "Read", "/etc/passwd", ""},
		{"other tools untouched", "risk-analyst", "Bash", "/etc/passwd", ""},
		// cwd is "/" in these invocations, and a recursive search rooted
		// there reaches every registered directory.
		{"main session Grep on empty path denied when cwd contains a fetch dir", "", "Grep", "", "deny"},
		{"main session Grep denied on fetch dir ancestor", "", "Grep", filepath.Dir(dataDir), "deny"},
		{"main session Glob denied on fetch dir ancestor", "", "Glob", filepath.Dir(dataDir), "deny"},
		{"main session Read untouched on fetch dir ancestor", "", "Read", filepath.Dir(dataDir), ""},
	}
	for _, c := range cases {
		out := invokeHook(t, c.agentType, c.tool, c.path)
		got := ""
		if strings.Contains(out, `"permissionDecision":"allow"`) {
			got = "allow"
		} else if strings.Contains(out, `"permissionDecision":"deny"`) {
			got = "deny"
		} else if out != "" {
			t.Errorf("%s: unexpected output %q", c.name, out)
			continue
		}
		if got != c.want {
			t.Errorf("%s: decision %q, want %q (output %q)", c.name, got, c.want, out)
		}
	}
}

func TestRunHookDeniesEverythingWithoutRegistry(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir()) // empty: no registry file

	out := invokeHook(t, "risk-analyst", "Read", "/tmp/soundings-abc123/index.json")
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("expected deny with no registry, got %q", out)
	}
}

func TestRunHookDeniesAfterRelease(t *testing.T) {
	dataDir := hookSetup(t)
	releaseDir(dataDir)

	out := invokeHook(t, "risk-analyst", "Read", filepath.Join(dataDir, "index.json"))
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("expected deny after release, got %q", out)
	}

	// The keep-out ends with the registration too: the data is gone, so
	// the normal permission flow applies again.
	if out := invokeHookFull(t, "", "Read", filepath.Join(dataDir, "index.json"), "", "/Users/someone/project"); out != "" {
		t.Errorf("expected no opinion for the main session after release, got %q", out)
	}
}

func TestRunHookGlobPattern(t *testing.T) {
	dataDir := hookSetup(t)
	benign := t.TempDir() // a cwd that contains no registered directory
	sibling := filepath.Join(filepath.Dir(dataDir), "benign-sibling")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name                     string
		agentType, path, pattern string
		want                     string
	}{
		{"main session denied on absolute pattern into fetch dir", "", "", filepath.Join(dataDir, "**", "*.patch"), "deny"},
		{"main session denied on absolute pattern over fetch dir ancestor", "", "", filepath.Join(filepath.Dir(dataDir), "soundings-*", "**"), "deny"},
		{"main session untouched on absolute pattern elsewhere", "", "", "/etc/**", ""},
		{"main session denied on relative pattern traversing into fetch dir", "", sibling, filepath.Join("..", filepath.Base(dataDir), "**"), "deny"},
		{"main session denied on case-variant absolute pattern", "", "", filepath.Join(caseVariant(dataDir), "**"), "deny"},
		{"risk-analyst denied on absolute pattern escaping fetch dir", "risk-analyst", dataDir, "/etc/**", "deny"},
		{"risk-analyst denied on relative pattern traversing out of fetch dir", "risk-analyst", dataDir, "../../../etc/*", "deny"},
		{"risk-analyst allowed on absolute pattern inside fetch dir", "risk-analyst", dataDir, filepath.Join(dataDir, "patches", "**"), "allow"},
		{"risk-analyst allowed on absolute in-bounds pattern with path omitted", "risk-analyst", "", filepath.Join(dataDir, "patches", "**"), "allow"},
		{"risk-analyst allowed on relative pattern with in-bounds path", "risk-analyst", dataDir, "patches/**/*.patch", "allow"},
		// Patterns whose reach a static prefix cannot bound are treated as
		// reaching anywhere: denied in both directions.
		{"main session denied on brace pattern", "", "", "{" + dataDir + ",x}/**", "deny"},
		{"risk-analyst denied on brace pattern", "risk-analyst", dataDir, "{a,b}/**", "deny"},
		// A separator-free group expands to filenames, not paths: bounded.
		{"main session untouched on filename brace pattern", "", "", "src/**/*.{ts,tsx}", ""},
		{"risk-analyst allowed on filename brace pattern", "risk-analyst", dataDir, "patches/**/*.{go,md}", "allow"},
		{"risk-analyst denied on dot-dot after wildcard", "risk-analyst", dataDir, "patches/**/../../other/*", "deny"},
		{"main session denied on dot-dot after wildcard", "", sibling, "*/../" + filepath.Base(dataDir) + "/**", "deny"},
	}
	for _, c := range cases {
		out := invokeHookFull(t, c.agentType, "Glob", c.path, c.pattern, benign)
		got := ""
		if strings.Contains(out, `"permissionDecision":"allow"`) {
			got = "allow"
		} else if strings.Contains(out, `"permissionDecision":"deny"`) {
			got = "deny"
		} else if out != "" {
			t.Errorf("%s: unexpected output %q", c.name, out)
			continue
		}
		if got != c.want {
			t.Errorf("%s: decision %q, want %q (output %q)", c.name, got, c.want, out)
		}
	}
}

func TestRunHookDeniesSymlinkEscapingDataDir(t *testing.T) {
	dataDir := hookSetup(t)

	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dataDir, "innocent.patch")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}

	out := invokeHook(t, "risk-analyst", "Read", link)
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("expected deny for symlink pointing outside the data dir, got %q", out)
	}
}

func TestRunHookAllowsUnresolvedFormOfRegisteredDir(t *testing.T) {
	// Register through a symlinked parent (like macOS /tmp -> /private/tmp)
	// and read through the same unresolved form: both sides canonicalize to
	// the same directory, so the read is allowed.
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "soundings-x"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkParent := filepath.Join(base, "alias")
	if err := os.Symlink(real, linkParent); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	if err := registerDir(filepath.Join(linkParent, "soundings-x")); err != nil {
		t.Fatalf("registerDir: %v", err)
	}

	out := invokeHook(t, "risk-analyst", "Read", filepath.Join(linkParent, "soundings-x", "index.json"))
	if !strings.Contains(out, `"permissionDecision":"allow"`) {
		t.Errorf("expected allow through symlinked parent, got %q", out)
	}
}

// caseVariant flips the case of a path's last component, naming the same
// directory on a case-insensitive filesystem.
func caseVariant(p string) string {
	base := strings.ToUpper(filepath.Base(p))
	if base == filepath.Base(p) {
		base = strings.ToLower(filepath.Base(p))
	}
	return filepath.Join(filepath.Dir(p), base)
}

func TestRunHookCaseVariantPathStaysFenced(t *testing.T) {
	dataDir := hookSetup(t)

	variant := filepath.Join(caseVariant(dataDir), "index.json")
	if out := invokeHook(t, "", "Read", variant); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("expected deny for a case-variant path into the fetch dir, got %q", out)
	}
	// The allow direction never folds: on a case-sensitive filesystem the
	// variant may be a distinct, attacker-creatable directory.
	if out := invokeHook(t, "risk-analyst", "Read", variant); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("expected deny for the risk-analyst on a case-variant path, got %q", out)
	}
}

func TestRunHookKeepOutOutlivesAuthorization(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dataDir := filepath.Join(t.TempDir(), "soundings-old")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := canonicalDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	writeEntry(t, resolved, time.Now().UTC().Add(-registryTTL-time.Hour))

	// The risk-analyst's authorization has expired...
	if out := invokeHook(t, "risk-analyst", "Read", filepath.Join(dataDir, "index.json")); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("expected deny for the risk-analyst on an expired registration, got %q", out)
	}
	// ...but the keep-out holds for as long as the untrusted data exists.
	if out := invokeHook(t, "", "Read", filepath.Join(dataDir, "index.json")); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("expected the keep-out to outlive the authorization, got %q", out)
	}
}

func TestRunHookErrorsOnMalformedInput(t *testing.T) {
	var out strings.Builder
	if err := runHook(strings.NewReader("not json"), &out); err == nil {
		t.Fatal("expected an error on malformed input")
	}
}
