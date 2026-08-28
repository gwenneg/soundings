package github

import "testing"

func TestShortSHA(t *testing.T) {
	tests := []struct {
		name     string
		sha      string
		expected string
	}{
		{"long sha", "abcdef1234567890", "abcdef12"},
		{"exactly 8 chars", "abcdef12", "abcdef12"},
		{"short sha", "abc", "abc"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortSHA(tt.sha); got != tt.expected {
				t.Errorf("shortSHA(%q) = %q, want %q", tt.sha, got, tt.expected)
			}
		})
	}
}
