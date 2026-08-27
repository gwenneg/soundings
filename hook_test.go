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
		{"assess denied outside data dir", "assess", "Read", "/Users/someone/.ssh/id_rsa", true},
		{"assess denied on empty path", "assess", "Read", "", true},
		{"assess denied on traversal", "assess", "Read", "/tmp/soundings-abc/../../home/x/.aws/credentials", true},
		{"plugin-namespaced assess denied", "soundings:assess", "Read", "/etc/passwd", true},
		{"assess allowed in data dir", "assess", "Read", "/tmp/soundings-abc123/index.json", false},
		{"assess allowed on patch file", "soundings:assess", "Read", "/var/folders/x1/T/soundings-42/patches/repo/001-main.go.patch", false},
		{"main session untouched", "", "Read", "/Users/someone/.ssh/id_rsa", false},
		{"other agents untouched", "Explore", "Read", "/Users/someone/.ssh/id_rsa", false},
		{"suffix must be a namespace", "harassess", "Read", "/etc/passwd", false},
		{"other tools untouched", "assess", "Grep", "/etc/passwd", false},
	}
	for _, c := range cases {
		in := fmt.Sprintf(`{"tool_name":%q,"agent_type":%q,"tool_input":{"file_path":%q}}`,
			c.tool, c.agentType, c.path)
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
