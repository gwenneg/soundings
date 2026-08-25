package shared

import (
	"testing"
)

func TestParseUserGuidance(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedText  string
		expectedFound bool
	}{
		{
			name:          "simple guidance",
			input:         "/soundings note this is a guidance message",
			expectedText:  "this is a guidance message",
			expectedFound: true,
		},
		{
			name:          "case insensitive",
			input:         "/SOUNDINGS NOTE This Is A Message",
			expectedText:  "This Is A Message",
			expectedFound: true,
		},
		{
			name:          "multiline guidance",
			input:         "/soundings note first line\nsecond line\nthird line",
			expectedText:  "first line\nsecond line\nthird line",
			expectedFound: true,
		},
		{
			name:          "with extra whitespace",
			input:         "  /soundings   note   guidance with spaces   ",
			expectedText:  "guidance with spaces",
			expectedFound: true,
		},
		{
			name:          "leading tabs should work",
			input:         "\t\t/soundings note guidance with tabs",
			expectedText:  "guidance with tabs",
			expectedFound: true,
		},
		{
			name:          "captures everything after subcommand",
			input:         "/soundings note first\nmore content\n/soundings note second",
			expectedText:  "first\nmore content\n/soundings note second",
			expectedFound: true,
		},
		{
			name:          "no guidance",
			input:         "Just some regular text without guidance",
			expectedText:  "",
			expectedFound: false,
		},
		{
			name:          "empty string",
			input:         "",
			expectedText:  "",
			expectedFound: false,
		},
		{
			name:          "bare /soundings no longer matches",
			input:         "/soundings this is guidance",
			expectedText:  "",
			expectedFound: false,
		},
		{
			name:          "soundings without space",
			input:         "/soundingsnote content",
			expectedText:  "",
			expectedFound: false,
		},
		{
			name:          "soundings note with only whitespace content",
			input:         "/soundings note   ",
			expectedText:  "",
			expectedFound: false,
		},
		{
			name:          "text before /soundings note should not match",
			input:         "Before\n/soundings note important guidance\nAfter",
			expectedText:  "",
			expectedFound: false,
		},
		{
			name:          "non-whitespace before /soundings note should not match",
			input:         "Some text /soundings note this should not match",
			expectedText:  "",
			expectedFound: false,
		},
		{
			name:          "inline /soundings note should not match",
			input:         "Please note: /soundings note this is important",
			expectedText:  "",
			expectedFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, found := ParseUserGuidance(tt.input)

			if found != tt.expectedFound {
				t.Errorf("found = %v, want %v", found, tt.expectedFound)
			}

			if text != tt.expectedText {
				t.Errorf("text = %q, want %q", text, tt.expectedText)
			}
		})
	}
}

func TestParseUserGuidanceEdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedText  string
		expectedFound bool
	}{
		{
			name:          "guidance with special characters",
			input:         "/soundings note Check the @deployment: it uses env vars!",
			expectedText:  "Check the @deployment: it uses env vars!",
			expectedFound: true,
		},
		{
			name:          "guidance with URLs",
			input:         "/soundings note See https://example.com for details",
			expectedText:  "See https://example.com for details",
			expectedFound: true,
		},
		{
			name:          "text before /soundings note should not match",
			input:         "Line 1\nLine 2\n/soundings note guidance here\nLine 3",
			expectedText:  "",
			expectedFound: false,
		},
		{
			name:          "multiple spaces after note subcommand",
			input:         "/soundings note     multiple     spaces",
			expectedText:  "multiple     spaces",
			expectedFound: true,
		},
		{
			name:          "non-whitespace before /soundings note should not match",
			input:         "Some text /soundings note this should not match",
			expectedText:  "",
			expectedFound: false,
		},
		{
			name:          "inline /soundings note should not match",
			input:         "Please note: /soundings note this is important",
			expectedText:  "",
			expectedFound: false,
		},
		{
			name:          "/soundings override does not match note pattern",
			input:         "/soundings override proceeding with justification",
			expectedText:  "",
			expectedFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, found := ParseUserGuidance(tt.input)

			if found != tt.expectedFound {
				t.Errorf("found = %v, want %v", found, tt.expectedFound)
			}

			if text != tt.expectedText {
				t.Errorf("text = %q, want %q", text, tt.expectedText)
			}
		})
	}
}
