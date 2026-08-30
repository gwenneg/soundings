package report

import (
	"fmt"
	"regexp"
)

// secretPatterns are deliberately conservative: well-known credential
// formats with distinctive prefixes, not generic high-entropy matching,
// so legitimate analysis text (commit hashes, base64 snippets in diffs)
// is never mangled. Character classes exclude quotes and backslashes, so
// a match cannot span a JSON string boundary.
var secretPatterns = []struct {
	label string
	re    *regexp.Regexp
}{
	{"github-token", regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`)},
	{"github-token", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}`)},
	{"gitlab-token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`)},
	{"aws-access-key-id", regexp.MustCompile(`\b(?:AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"anthropic-api-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`)},
	{"openai-api-key", regexp.MustCompile(`\bsk-proj-[A-Za-z0-9_-]{20,}`)},
	{"npm-token", regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	// A full PEM block first (non-greedy, needs the END marker), then a
	// bare header as fallback. In pathological input the block form can
	// span JSON structure; redaction runs before schema validation, so
	// that surfaces as a validation error (fail closed), never as a
	// leaked key.
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)},
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
}

// RedactSecrets replaces recognizable credentials in s with
// [REDACTED:<kind>] markers and reports how many were replaced. The
// risk-analyst agent is instructed never to include secrets in its analysis,
// but that instruction is advisory and its input is externally authored;
// this pass makes the report side of that promise deterministic. The
// replacement text is safe inside a JSON string, so redacting the raw
// analysis JSON cannot itself corrupt a well-formed value.
func RedactSecrets(s string) (string, int) {
	total := 0
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllStringFunc(s, func(string) string {
			total++
			return fmt.Sprintf("[REDACTED:%s]", p.label)
		})
	}
	return s, total
}
