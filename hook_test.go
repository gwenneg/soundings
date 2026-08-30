package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	in := fmt.Sprintf(`{"tool_name":%q,"agent_type":%q,"cwd":"/","tool_input":{"file_path":%q,"path":%q}}`,
		tool, agentType, path, path)
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
		{"main session untouched", "", "Read", "/Users/someone/.ssh/id_rsa", ""},
		{"other agents untouched", "Explore", "Read", "/Users/someone/.ssh/id_rsa", ""},
		{"prefix without namespace untouched", "harisk-analyst", "Read", "/etc/passwd", ""},
		{"other plugin's risk-analyst untouched", "otherplugin:risk-analyst", "Read", dataDir, ""},
		{"other tools untouched", "risk-analyst", "Bash", "/etc/passwd", ""},
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

func TestRunHookWriteAndEdit(t *testing.T) {
	dataDir := hookSetup(t)

	report := filepath.Join(t.TempDir(), "soundings-report.md")
	if err := os.WriteFile(report, []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := registerFile(report); err != nil {
		t.Fatalf("registerFile: %v", err)
	}

	cases := []struct {
		name, tool, path string
		want             string
	}{
		{"Edit on registered report file allowed", "Edit", report, "allow"},
		{"Write on registered report file allowed", "Write", report, "allow"},
		{"Write inside registered data dir allowed", "Write", filepath.Join(dataDir, "analysis.json"), "allow"},
		{"Edit elsewhere gets no opinion", "Edit", "/Users/someone/notes.md", ""},
		{"Write elsewhere gets no opinion", "Write", "/Users/someone/notes.md", ""},
		{"Write on unregistered sibling gets no opinion", "Write", filepath.Join(filepath.Dir(report), "other.md"), ""},
	}
	for _, c := range cases {
		// Main-session calls: agent_type is empty.
		out := invokeHook(t, "", c.tool, c.path)
		got := ""
		if strings.Contains(out, `"permissionDecision":"allow"`) {
			got = "allow"
		} else if out != "" {
			t.Errorf("%s: unexpected output %q", c.name, out)
			continue
		}
		if got != c.want {
			t.Errorf("%s: decision %q, want %q", c.name, got, c.want)
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

func TestRunHookDeniesAfterDeregistration(t *testing.T) {
	dataDir := hookSetup(t)
	if err := deregisterDir(dataDir); err != nil {
		t.Fatalf("deregisterDir: %v", err)
	}

	out := invokeHook(t, "risk-analyst", "Read", filepath.Join(dataDir, "index.json"))
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("expected deny after deregistration, got %q", out)
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

func TestRunHookErrorsOnMalformedInput(t *testing.T) {
	var out strings.Builder
	if err := runHook(strings.NewReader("not json"), &out); err == nil {
		t.Fatal("expected an error on malformed input")
	}
}
