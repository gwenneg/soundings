package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestRunHook(t *testing.T) {
	cases := []struct {
		name                  string
		agentType, tool, path string
		wantDeny              bool
	}{
		{"risk-analyst denied outside data dir", "risk-analyst", "Read", "/Users/someone/.ssh/id_rsa", true},
		{"risk-analyst denied on empty path", "risk-analyst", "Read", "", true},
		{"risk-analyst denied on traversal", "risk-analyst", "Read", "/tmp/soundings-abc/../../home/x/.aws/credentials", true},
		{"plugin-namespaced risk-analyst denied", "soundings:risk-analyst", "Read", "/etc/passwd", true},
		{"risk-analyst allowed in data dir", "risk-analyst", "Read", "/tmp/soundings-abc123/index.json", false},
		{"risk-analyst allowed on patch file", "soundings:risk-analyst", "Read", "/var/folders/x1/T/soundings-42/patches/repo/001-main.go.patch", false},
		{"risk-analyst allowed on compare-range slug with literal dots", "soundings:risk-analyst", "Read", "/tmp/soundings-2992028217/patches/notifications-gw_compare_7aa0d9f0e3c06af5ce556dd2110a3548bb27d87a...0a45e834e08306506fb11dbcf06d591531fca9b7-abcd1234/000.patch", false},
		{"main session untouched", "", "Read", "/Users/someone/.ssh/id_rsa", false},
		{"other agents untouched", "Explore", "Read", "/Users/someone/.ssh/id_rsa", false},
		{"suffix must be a namespace", "harisk-analyst", "Read", "/etc/passwd", false},
		{"other tools untouched", "risk-analyst", "Bash", "/etc/passwd", false},
		{"risk-analyst denied Grep outside data dir", "risk-analyst", "Grep", "/etc/passwd", true},
		{"risk-analyst denied Grep on empty path", "risk-analyst", "Grep", "", true},
		{"risk-analyst allowed Grep in data dir", "soundings:risk-analyst", "Grep", "/tmp/soundings-abc123/patches", false},
		{"risk-analyst denied Glob outside data dir", "risk-analyst", "Glob", "/home/x", true},
		{"risk-analyst allowed Glob in data dir", "soundings:risk-analyst", "Glob", "/tmp/soundings-abc123", false},
	}
	for _, c := range cases {
		in := fmt.Sprintf(`{"tool_name":%q,"agent_type":%q,"tool_input":{"file_path":%q,"path":%q}}`,
			c.tool, c.agentType, c.path, c.path)
		var out strings.Builder
		if err := runHook(strings.NewReader(in), &out); err != nil {
			t.Errorf("%s: runHook: %v", c.name, err)
			continue
		}
		denied := strings.Contains(out.String(), `"permissionDecision":"deny"`)
		if denied != c.wantDeny {
			t.Errorf("%s: deny=%v, want %v (output %q)", c.name, denied, c.wantDeny, out.String())
		}
	}
}

func TestRunHookErrorsOnMalformedInput(t *testing.T) {
	var out strings.Builder
	if err := runHook(strings.NewReader("not json"), &out); err == nil {
		t.Fatal("expected an error on malformed input")
	}
}
