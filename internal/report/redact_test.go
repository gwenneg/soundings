package report

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactSecretsReplacesKnownFormats(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		label string
	}{
		{"github classic", "token ghp_abcdefghijklmnopqrstuvwxyz0123456789 leaked", "github-token"},
		{"github fine-grained", "github_pat_11ABCDEFG0_abcdefghijklmnopqrstuvwxyz", "github-token"},
		{"gitlab", "glpat-abcdefghij1234567890x", "gitlab-token"},
		{"aws", "key AKIAIOSFODNN7EXAMPLE in config", "aws-access-key-id"},
		{"slack", "xoxb-1234567890-abcdefghijk", "slack-token"},
		{"anthropic", "sk-ant-api03-abcdefghijklmnopqrstuv", "anthropic-api-key"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9P", "jwt"},
		{"pem block", "-----BEGIN RSA PRIVATE KEY-----\\nMIIEow\\n-----END RSA PRIVATE KEY-----", "private-key"},
		{"pem header only", "starts with -----BEGIN PRIVATE KEY----- and got cut off", "private-key"},
	}
	for _, c := range cases {
		out, n := RedactSecrets(c.in)
		if n == 0 {
			t.Errorf("%s: nothing redacted in %q", c.name, c.in)
			continue
		}
		if !strings.Contains(out, "[REDACTED:"+c.label+"]") {
			t.Errorf("%s: expected [REDACTED:%s] marker, got %q", c.name, c.label, out)
		}
	}
}

func TestRedactSecretsLeavesOrdinaryTextAlone(t *testing.T) {
	for _, in := range []string{
		"commit 76dffc9a1b2c3d4e5f60718293a4b5c6d7e8f901 touches auth/handler.go",
		"severity raised because internal/http/httpclient.go changes retry timeouts",
		"the sk-learn dependency was bumped",           // not an API key prefix
		"AKIA is mentioned in the docs",                // too short for a key id
		"config key `gitlab_token` read from the env",  // a name, not a value
		"base64 padding like aGVsbG8gd29ybGQ= in diff", // generic base64
	} {
		out, n := RedactSecrets(in)
		if n != 0 || out != in {
			t.Errorf("expected %q untouched, got %q (%d redactions)", in, out, n)
		}
	}
}

func TestRedactSecretsKeepsJSONValid(t *testing.T) {
	analysis := map[string]any{
		"summary": "diff adds ghp_abcdefghijklmnopqrstuvwxyz0123456789 to CI config",
		"concern": "hardcoded AKIAIOSFODNN7EXAMPLE in deploy.sh",
	}
	raw, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	out, n := RedactSecrets(string(raw))
	if n != 2 {
		t.Fatalf("expected 2 redactions, got %d", n)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(out), &back); err != nil {
		t.Fatalf("redacted output is no longer valid JSON: %v\n%s", err, out)
	}
	if s := back["summary"].(string); strings.Contains(s, "ghp_") {
		t.Errorf("token survived redaction: %q", s)
	}
}
