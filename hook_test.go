package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func runHookOn(t *testing.T, input string) string {
	t.Helper()
	var out strings.Builder
	if err := runHook(strings.NewReader(input), &out); err != nil {
		t.Fatalf("runHook: %v", err)
	}
	return out.String()
}

func hookInputJSON(t *testing.T, agentType, toolName, filePath string) string {
	t.Helper()
	in := map[string]any{
		"tool_name":  toolName,
		"tool_input": map[string]any{"file_path": filePath},
	}
	if agentType != "" {
		in["agent_type"] = agentType
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertDenied(t *testing.T, out string) {
	t.Helper()
	var d hookDecision
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("expected a decision JSON, got %q: %v", out, err)
	}
	if d.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("expected deny, got %q", d.HookSpecificOutput.PermissionDecision)
	}
	if d.HookSpecificOutput.PermissionDecisionReason == "" {
		t.Fatal("deny decision must carry a reason")
	}
}

func TestHookDeniesAssessReadOutsideDataDir(t *testing.T) {
	for _, path := range []string{
		"/Users/someone/.ssh/id_rsa",
		"/home/someone/project/.env",
		"/etc/passwd",
		"",
	} {
		out := runHookOn(t, hookInputJSON(t, "assess", "Read", path))
		if out == "" {
			t.Fatalf("expected deny for %q, hook stayed silent", path)
		}
		assertDenied(t, out)
	}
}

func TestHookDeniesTraversalOutOfDataDir(t *testing.T) {
	out := runHookOn(t, hookInputJSON(t, "assess", "Read",
		"/tmp/soundings-abc123/../../home/someone/.aws/credentials"))
	if out == "" {
		t.Fatal("expected deny for a path traversing out of the data dir")
	}
	assertDenied(t, out)
}

func TestHookAllowsAssessReadInDataDir(t *testing.T) {
	for _, path := range []string{
		"/tmp/soundings-abc123/index.json",
		"/var/folders/x1/T/soundings-42/patches/github.com_org_repo/001-main.go.patch",
		"/tmp/soundings-abc123/docs/github.com_org_repo/main-README.md.md",
	} {
		if out := runHookOn(t, hookInputJSON(t, "assess", "Read", path)); out != "" {
			t.Fatalf("expected silence (no opinion) for %q, got %q", path, out)
		}
	}
}

func TestHookMatchesPluginNamespacedAgentType(t *testing.T) {
	out := runHookOn(t, hookInputJSON(t, "soundings:assess", "Read", "/etc/passwd"))
	if out == "" {
		t.Fatal("expected deny for the plugin-namespaced assess agent")
	}
	assertDenied(t, out)
}

func TestHookIgnoresOtherCallers(t *testing.T) {
	// Main session (no agent_type) and other agents keep their normal
	// permission flow, even for sensitive paths.
	for _, agentType := range []string{"", "Explore", "harassess"} {
		if out := runHookOn(t, hookInputJSON(t, agentType, "Read", "/Users/someone/.ssh/id_rsa")); out != "" {
			t.Fatalf("expected silence for agent_type %q, got %q", agentType, out)
		}
	}
}

func TestHookIgnoresOtherTools(t *testing.T) {
	if out := runHookOn(t, hookInputJSON(t, "assess", "Grep", "/etc/passwd")); out != "" {
		t.Fatalf("expected silence for non-Read tool, got %q", out)
	}
}

func TestHookErrorsOnMalformedInput(t *testing.T) {
	var out strings.Builder
	if err := runHook(strings.NewReader("not json"), &out); err == nil {
		t.Fatal("expected an error on malformed input")
	}
}
