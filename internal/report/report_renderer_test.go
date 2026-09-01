package report

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gwenneg/soundings/internal/git/types"
)

// Test utility functions

func TestStripMarkdownCodeBlocks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no code blocks",
			input:    `{"key": "value"}`,
			expected: `{"key": "value"}`,
		},
		{
			name:     "json code block",
			input:    "```json\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
		{
			name:     "plain code block",
			input:    "```\n{\"key\": \"value\"}\n```",
			expected: `{"key": "value"}`,
		},
		{
			name:     "with whitespace",
			input:    "  ```json\n{\"key\": \"value\"}\n```  ",
			expected: `{"key": "value"}`,
		},
		{
			name:     "multiline json",
			input:    "```json\n{\n  \"key\": \"value\",\n  \"other\": 123\n}\n```",
			expected: "{\n  \"key\": \"value\",\n  \"other\": 123\n}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripMarkdownCodeBlocks(tt.input)
			if result != tt.expected {
				t.Errorf("StripMarkdownCodeBlocks() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// Test template helper functions

func TestEscapePipes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"no pipes", "hello world", "hello world"},
		{"single pipe", "hello|world", "hello\\|world"},
		{"multiple pipes", "a|b|c", "a\\|b\\|c"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapePipes(tt.input)
			if result != tt.expected {
				t.Errorf("escapePipes(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestAuthorizationStatus(t *testing.T) {
	tests := []struct {
		name         string
		isAuthorized bool
		expected     string
	}{
		{"authorized", true, "✅ Authorized"},
		{"unauthorized", false, "❌ Unauthorized"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := authorizationStatus(tt.isAuthorized)
			if result != tt.expected {
				t.Errorf("authorizationStatus(%v) = %q, want %q", tt.isAuthorized, result, tt.expected)
			}
		})
	}
}

func TestGuidanceStatus(t *testing.T) {
	tests := []struct {
		name     string
		guidance types.UserGuidance
		expected string
	}{
		{"authorized", types.UserGuidance{IsAuthorized: true}, "✅ Authorized"},
		{"unauthorized", types.UserGuidance{IsAuthorized: false}, "❌ Unauthorized"},
		{"external takes precedence over authorized", types.UserGuidance{IsAuthorized: true, IsExternal: true}, "🌐 External (not analyzed)"},
		{"external takes precedence over unauthorized", types.UserGuidance{IsAuthorized: false, IsExternal: true}, "🌐 External (not analyzed)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := guidanceStatus(tt.guidance)
			if result != tt.expected {
				t.Errorf("guidanceStatus(%+v) = %q, want %q", tt.guidance, result, tt.expected)
			}
		})
	}
}

func TestPRLink(t *testing.T) {
	tests := []struct {
		name     string
		prNumber int64
		repoURL  string
		platform string
		expected string
	}{
		{"valid PR", 123, "https://github.com/user/repo", "github", "[#123](https://github.com/user/repo/pull/123)"},
		{"gitlab MR", 7, "https://gitlab.example.com/group/sub/repo", "gitlab", "[!7](https://gitlab.example.com/group/sub/repo/-/merge_requests/7)"},
		{"zero PR", 0, "https://github.com/user/repo", "github", "N/A"},
		{"negative PR", -1, "https://github.com/user/repo", "github", "N/A"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := prLink(tt.prNumber, tt.repoURL, tt.platform)
			if result != tt.expected {
				t.Errorf("prLink(%d, %q, %q) = %q, want %q", tt.prNumber, tt.repoURL, tt.platform, result, tt.expected)
			}
		})
	}
}

func TestFormatAuthor(t *testing.T) {
	tests := []struct {
		name       string
		author     string
		commentURL string
		expected   string
	}{
		{"github user", "johndoe", "https://github.com/owner/repo/pull/1#comment", "[@johndoe](https://github.com/johndoe)"},
		{"gitlab user", "janedoe", "https://gitlab.com/owner/repo/-/merge_requests/1#note", "@janedoe"},
		{"other platform", "user", "https://example.com/comment/1", "@user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatAuthor(tt.author, tt.commentURL)
			if result != tt.expected {
				t.Errorf("formatAuthor(%q, %q) = %q, want %q", tt.author, tt.commentURL, result, tt.expected)
			}
		})
	}
}

func TestDocURL(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		repoURL  string
		branch   string
		platform string
		expected string
	}{
		{
			"relative path",
			"README.md",
			"https://github.com/user/repo",
			"main",
			"github",
			"https://github.com/user/repo/blob/main/README.md",
		},
		{
			"gitlab subgroup path uses /-/ scope",
			".soundings-docs.md",
			"https://gitlab.example.com/group/sub/repo",
			"main",
			"gitlab",
			"https://gitlab.example.com/group/sub/repo/-/blob/main/.soundings-docs.md",
		},
		{
			"http URL",
			"http://example.com/doc.md",
			"https://github.com/user/repo",
			"main",
			"github",
			"http://example.com/doc.md",
		},
		{
			"https URL",
			"https://example.com/doc.md",
			"https://github.com/user/repo",
			"main",
			"github",
			"https://example.com/doc.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := docURL(tt.filename, tt.repoURL, tt.branch, tt.platform)
			if result != tt.expected {
				t.Errorf("docURL(%q, %q, %q, %q) = %q, want %q", tt.filename, tt.repoURL, tt.branch, tt.platform, result, tt.expected)
			}
		})
	}
}

func TestCommitLink(t *testing.T) {
	tests := []struct {
		name     string
		shortSHA string
		fullSHA  string
		repoURL  string
		platform string
		expected string
	}{
		{
			"standard commit",
			"abc123",
			"abc123def456",
			"https://github.com/user/repo",
			"github",
			"[abc123](https://github.com/user/repo/commit/abc123def456)",
		},
		{
			"gitlab commit uses /-/ scope",
			"abc123",
			"abc123def456",
			"https://gitlab.example.com/group/sub/repo",
			"gitlab",
			"[abc123](https://gitlab.example.com/group/sub/repo/-/commit/abc123def456)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := commitLink(tt.shortSHA, tt.fullSHA, tt.repoURL, tt.platform)
			if result != tt.expected {
				t.Errorf("commitLink(%q, %q, %q, %q) = %q, want %q", tt.shortSHA, tt.fullSHA, tt.repoURL, tt.platform, result, tt.expected)
			}
		})
	}
}

func TestFormatDate(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{
			"standard date",
			time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC),
			"2024-01-15 14:30",
		},
		{
			"zero time",
			time.Time{},
			"0001-01-01 00:00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDate(tt.input)
			if result != tt.expected {
				t.Errorf("formatDate(%v) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestDocFileInfo(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		repoURL  string
		branch   string
		content  string
		expected string
	}{
		{
			"standard file",
			"README.md",
			"https://github.com/user/repo",
			"main",
			"This is content",
			"- https://github.com/user/repo/blob/main/README.md - 15 chars",
		},
		{
			"external URL",
			"https://example.com/doc.md",
			"https://github.com/user/repo",
			"main",
			"Content",
			"- https://example.com/doc.md - 7 chars",
		},
		{
			"empty content",
			"empty.md",
			"https://github.com/user/repo",
			"main",
			"",
			"- https://github.com/user/repo/blob/main/empty.md - 0 chars",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := docFileInfo(tt.filename, tt.repoURL, tt.branch, "github", tt.content)
			if result != tt.expected {
				t.Errorf("docFileInfo(%q, %q, %q, %q) = %q, want %q", tt.filename, tt.repoURL, tt.branch, tt.content, result, tt.expected)
			}
		})
	}
}

func TestComputeVerdict(t *testing.T) {
	concern := func(severity string) RiskConcern {
		return RiskConcern{Severity: severity, Description: severity + " issue"}
	}
	tests := []struct {
		name                string
		concerns            []RiskConcern
		criticalActionItems []string
		blockOn             string
		wantVerdict         Verdict
		wantReasons         []string
	}{
		{
			"no concerns",
			nil, nil, "critical",
			VerdictRelease, nil,
		},
		{
			"low and medium only release",
			[]RiskConcern{concern("low"), concern("medium")}, nil, "critical",
			VerdictRelease, nil,
		},
		{
			"high requires review",
			[]RiskConcern{concern("medium"), concern("high")}, nil, "critical",
			VerdictReview, []string{"⚠️ high issue"},
		},
		{
			"critical blocks and only blocking concerns are reasons",
			[]RiskConcern{concern("high"), concern("critical")}, nil, "critical",
			VerdictNotRecommended, []string{"🔥 critical issue"},
		},
		{
			"critical action items escalate release to review",
			[]RiskConcern{concern("medium")}, []string{"run the load test"}, "critical",
			VerdictReview, []string{"📋 Complete before release: run the load test"},
		},
		{
			"critical action items do not downgrade a no-go",
			[]RiskConcern{concern("critical")}, []string{"x"}, "critical",
			VerdictNotRecommended, []string{"🔥 critical issue"},
		},
		{
			"block_on high: high blocks",
			[]RiskConcern{concern("high")}, nil, "high",
			VerdictNotRecommended, []string{"⚠️ high issue"},
		},
		{
			"block_on high: medium requires review",
			[]RiskConcern{concern("medium")}, nil, "high",
			VerdictReview, []string{"🟡 medium issue"},
		},
		{
			"block_on medium: low requires review",
			[]RiskConcern{concern("low")}, nil, "medium",
			VerdictReview, []string{"🟢 low issue"},
		},
		{
			"unknown severity treated as critical",
			[]RiskConcern{{Severity: "urgent", Description: "odd"}}, nil, "critical",
			VerdictNotRecommended, []string{"🔥 odd"},
		},
		{
			"unknown block_on falls back to critical",
			[]RiskConcern{concern("high")}, nil, "bogus",
			VerdictReview, []string{"⚠️ high issue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, reasons := ComputeVerdict(tt.concerns, tt.criticalActionItems, tt.blockOn)
			if verdict != tt.wantVerdict {
				t.Errorf("verdict = %q, want %q", verdict, tt.wantVerdict)
			}
			if !reflect.DeepEqual(reasons, tt.wantReasons) {
				t.Errorf("reasons = %#v, want %#v", reasons, tt.wantReasons)
			}
		})
	}
}

func TestTemplateFuncs(t *testing.T) {
	funcs := templateFuncs()

	expectedFuncs := []string{
		"hasPrefix",
		"escapePipes",
		"escapeCell",
		"severityEmoji",
		"authorizationStatus",
		"prLink",
		"formatAuthor",
		"docURL",
		"commitLink",
		"formatDate",
		"docFileInfo",
	}

	for _, name := range expectedFuncs {
		if _, ok := funcs[name]; !ok {
			t.Errorf("templateFuncs() missing expected function: %s", name)
		}
	}
}

func TestGenerateReport(t *testing.T) {
	// Test with minimal valid JSON (v2 schema)
	minimalJSON := `{
		"summary": "Bug fix with low impact",
		"risk_summary": {
			"concerns": [],
			"positives": ["Well tested"]
		},
		"action_items": {
			"critical": [],
			"important": [],
			"followup": []
		},
		"technical_details": {
			"code": ["Code looks good"],
			"infrastructure": [],
			"dependencies": []
		},
		"documentation_quality": "Good",
		"documentation_recommendations": "None"
	}`

	config := &ReportConfig{
		LLMResponse:   minimalJSON,
		Metadata:      &ReportMetadata{ModelID: "test-model", GenerationTime: time.Now()},
		Comparisons:   nil,
		Documentation: nil,
		UserGuidance:  nil,
	}

	verdict, report, err := GenerateReport(config)
	if err != nil {
		t.Fatalf("GenerateReport() error = %v", err)
	}

	if verdict != VerdictRelease {
		t.Errorf("GenerateReport() verdict = %q, want %q", verdict, VerdictRelease)
	}

	if report == "" {
		t.Error("GenerateReport() returned empty report")
	}

	// Check that the report contains expected sections
	expectedSections := []string{
		"Release Readiness Report",
		"Recommended for release",
		"No blocking concerns found.",
		"Technical Details",
		"Code Changes",
	}

	for _, section := range expectedSections {
		if !strings.Contains(report, section) {
			t.Errorf("GenerateReport() report missing section: %q", section)
		}
	}
}

func TestGenerateReportWithTruncationInfo(t *testing.T) {
	minimalJSON := `{
		"summary": "Bug fix with low impact",
		"risk_summary": {"concerns": [], "positives": []},
		"action_items": {"critical": [], "important": [], "followup": []},
		"technical_details": {"code": [], "infrastructure": [], "dependencies": []},
		"documentation_quality": "Good",
		"documentation_recommendations": "None"
	}`

	config := &ReportConfig{
		LLMResponse: minimalJSON,
		Metadata:    &ReportMetadata{ModelID: "test-model", GenerationTime: time.Now()},
		TruncationInfo: &TruncationInfo{
			TotalFiles:     3,
			FilesPreserved: 2,
			FilesTruncated: 1,
			TruncatedFiles: []string{"package-lock.json"},
		},
	}

	_, report, err := GenerateReport(config)
	if err != nil {
		t.Fatalf("GenerateReport() error = %v", err)
	}

	for _, want := range []string{"Diff Truncation Applied", "2/3", "package-lock.json"} {
		if !strings.Contains(report, want) {
			t.Errorf("GenerateReport() report missing %q", want)
		}
	}
}

func TestGenerateReportWithoutTruncationInfo(t *testing.T) {
	minimalJSON := `{
		"summary": "Bug fix with low impact",
		"risk_summary": {"concerns": [], "positives": []},
		"action_items": {"critical": [], "important": [], "followup": []},
		"technical_details": {"code": [], "infrastructure": [], "dependencies": []},
		"documentation_quality": "Good",
		"documentation_recommendations": "None"
	}`

	config := &ReportConfig{
		LLMResponse: minimalJSON,
		Metadata:    &ReportMetadata{ModelID: "test-model", GenerationTime: time.Now()},
	}

	_, report, err := GenerateReport(config)
	if err != nil {
		t.Fatalf("GenerateReport() error = %v", err)
	}

	if strings.Contains(report, "Diff Truncation Applied") {
		t.Error("GenerateReport() report should not mention truncation when TruncationInfo is nil")
	}
}

func TestGenerateReportInvalidJSON(t *testing.T) {
	config := &ReportConfig{
		LLMResponse: "not valid json",
		Metadata:    &ReportMetadata{ModelID: "test-model", GenerationTime: time.Now()},
	}

	_, _, err := GenerateReport(config)
	if err == nil {
		t.Error("GenerateReport() expected error for invalid JSON, got nil")
	}
}

func TestGenerateReportWithUserGuidance(t *testing.T) {
	jsonResponse := `{
		"summary": "New feature addition with medium impact",
		"risk_summary": {
			"concerns": [{"severity": "medium", "description": "Needs testing"}],
			"positives": ["Well structured", "Clean code"]
		},
		"action_items": {
			"critical": ["Test thoroughly"],
			"important": ["Update docs"],
			"followup": []
		},
		"technical_details": {
			"code": ["New feature added"],
			"infrastructure": [],
			"dependencies": []
		},
		"documentation_quality": "Adequate",
		"documentation_recommendations": "Add examples"
	}`

	// Create user guidance with different dates to test sorting
	userGuidance := []types.UserGuidance{
		{
			Content:      "Third guidance",
			Author:       "user3",
			Date:         time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC),
			CommentURL:   "https://github.com/owner/repo/pull/1#comment3",
			IsAuthorized: true,
		},
		{
			Content:      "First guidance",
			Author:       "user1",
			Date:         time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			CommentURL:   "https://github.com/owner/repo/pull/1#comment1",
			IsAuthorized: true,
		},
		{
			Content:      "Second guidance",
			Author:       "user2",
			Date:         time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
			CommentURL:   "https://gitlab.com/owner/repo/-/merge_requests/1#note",
			IsAuthorized: false,
		},
	}

	config := &ReportConfig{
		LLMResponse:  jsonResponse,
		Metadata:     &ReportMetadata{ModelID: "test-model", GenerationTime: time.Now()},
		UserGuidance: userGuidance,
	}

	verdict, report, err := GenerateReport(config)
	if err != nil {
		t.Fatalf("GenerateReport() error = %v", err)
	}

	// The medium concern doesn't hold the release, but the outstanding
	// critical action item ("Test thoroughly") escalates to manual review.
	if verdict != VerdictReview {
		t.Errorf("GenerateReport() verdict = %q, want %q", verdict, VerdictReview)
	}
	if !strings.Contains(report, "📋 Complete before release: Test thoroughly") {
		t.Error("GenerateReport() report missing the action-item verdict reason")
	}

	// Verify user guidance section exists
	if !strings.Contains(report, "User Guidance") {
		t.Error("GenerateReport() report missing User Guidance section")
	}

	// Verify guidance is included
	if !strings.Contains(report, "First guidance") {
		t.Error("GenerateReport() report missing first guidance")
	}
	if !strings.Contains(report, "Second guidance") {
		t.Error("GenerateReport() report missing second guidance")
	}
	if !strings.Contains(report, "Third guidance") {
		t.Error("GenerateReport() report missing third guidance")
	}

	// Verify authorization status
	if !strings.Contains(report, "✅ Authorized") {
		t.Error("GenerateReport() report missing authorized status")
	}
	if !strings.Contains(report, "❌ Unauthorized") {
		t.Error("GenerateReport() report missing unauthorized status")
	}
}

func TestGenerateReportWithComparisons(t *testing.T) {
	jsonResponse := `{
		"summary": "Bug fix with low impact",
		"risk_summary": {"concerns": [], "positives": ["Tested"]},
		"action_items": {"critical": [], "important": [], "followup": []},
		"technical_details": {"code": ["Good"], "infrastructure": [], "dependencies": []},
		"documentation_quality": "Good",
		"documentation_recommendations": "None"
	}`

	comparisons := []*types.Comparison{
		{
			Platform: "github",
			RepoURL:  "https://github.com/user/repo",
			DiffURL:  "https://github.com/user/repo/compare/v1...v2",
			Commits: []types.Commit{
				{
					SHA:      "abc123def456",
					ShortSHA: "abc123",
					Message:  "Fix bug | with pipe",
					Author:   "John Doe",
					PRNumber: 123,
				},
				{
					SHA:      "def456abc789",
					ShortSHA: "def456",
					Message:  "Another fix",
					Author:   "Jane Smith",
					PRNumber: 0,
				},
			},
			Files: []types.FileChange{},
			Stats: types.ComparisonStats{},
		},
	}

	config := &ReportConfig{
		LLMResponse: jsonResponse,
		Metadata:    &ReportMetadata{ModelID: "test-model", GenerationTime: time.Now()},
		Comparisons: comparisons,
	}

	verdict, report, err := GenerateReport(config)
	if err != nil {
		t.Fatalf("GenerateReport() error = %v", err)
	}

	if verdict != VerdictRelease {
		t.Errorf("GenerateReport() verdict = %q, want %q", verdict, VerdictRelease)
	}

	// Verify changelog section
	if !strings.Contains(report, "Changelogs") {
		t.Error("GenerateReport() report missing Changelogs section")
	}

	// Verify commits are included
	if !strings.Contains(report, "abc123") {
		t.Error("GenerateReport() report missing first commit")
	}
	if !strings.Contains(report, "def456") {
		t.Error("GenerateReport() report missing second commit")
	}

	// Verify pipe escaping
	if !strings.Contains(report, "Fix bug \\| with pipe") {
		t.Error("GenerateReport() report did not escape pipes in commit message")
	}

	// Verify PR link
	if !strings.Contains(report, "#123") {
		t.Error("GenerateReport() report missing PR link")
	}
	if !strings.Contains(report, "N/A") {
		t.Error("GenerateReport() report missing N/A for commit without PR")
	}
}

func TestGenerateReportWithDocumentation(t *testing.T) {
	jsonResponse := `{
		"summary": "Documentation update with low impact",
		"risk_summary": {"concerns": [], "positives": ["Well documented"]},
		"action_items": {"critical": [], "important": [], "followup": []},
		"technical_details": {"code": [], "infrastructure": [], "dependencies": []},
		"documentation_quality": "Excellent",
		"documentation_recommendations": "Keep it up"
	}`

	docs := []*types.Documentation{
		{
			Repository: types.Repository{
				URL:           "https://github.com/user/repo",
				DefaultBranch: "main",
			},
			MainDocFile:         "README.md",
			MainDocContent:      "# Project\n\nDescription",
			AdditionalDocsOrder: []string{"CONTRIBUTING.md", "https://example.com/external-doc.md"},
			AdditionalDocsContent: map[string]string{
				"CONTRIBUTING.md":                     "Contribution guidelines",
				"https://example.com/external-doc.md": "External documentation",
			},
		},
	}

	config := &ReportConfig{
		LLMResponse:   jsonResponse,
		Metadata:      &ReportMetadata{ModelID: "test-model", GenerationTime: time.Now()},
		Documentation: docs,
	}

	verdict, report, err := GenerateReport(config)
	if err != nil {
		t.Fatalf("GenerateReport() error = %v", err)
	}

	if verdict != VerdictRelease {
		t.Errorf("GenerateReport() verdict = %q, want %q", verdict, VerdictRelease)
	}

	// Verify documentation section
	if !strings.Contains(report, "Documentation Sources Analyzed") {
		t.Error("GenerateReport() report missing Documentation Sources section")
	}

	// Verify files are listed
	if !strings.Contains(report, "README.md") {
		t.Error("GenerateReport() report missing README.md")
	}
	if !strings.Contains(report, "CONTRIBUTING.md") {
		t.Error("GenerateReport() report missing CONTRIBUTING.md")
	}

	// Verify external URL is preserved
	if !strings.Contains(report, "https://example.com/external-doc.md") {
		t.Error("GenerateReport() report missing external doc URL")
	}

	// Verify char counts
	if !strings.Contains(report, "chars") {
		t.Error("GenerateReport() report missing char counts")
	}
}

func TestGenerateReportPrefersAnalysisModel(t *testing.T) {
	jsonResponse := `{
		"model": "claude-test-9",
		"summary": "s",
		"risk_summary": {"concerns": [], "positives": []},
		"action_items": {"critical": [], "important": [], "followup": []},
		"technical_details": {"code": [], "infrastructure": [], "dependencies": []},
		"documentation_quality": "ok",
		"documentation_recommendations": "none"
	}`

	config := &ReportConfig{
		LLMResponse: jsonResponse,
		Metadata:    &ReportMetadata{ModelID: "flag-model", GenerationTime: time.Now()},
	}

	_, report, err := GenerateReport(config)
	if err != nil {
		t.Fatalf("GenerateReport() error = %v", err)
	}
	if !strings.Contains(report, "claude-test-9") {
		t.Error("report footer should use the model stated in the analysis")
	}
	if strings.Contains(report, "flag-model") {
		t.Error("report footer should not use the --model-id fallback when the analysis states a model")
	}
}

func TestStripMarkdownCodeBlocksProseTolerance(t *testing.T) {
	fence := "\x60\x60\x60"
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"plain JSON untouched", "{\"a\": 1}", "{\"a\": 1}"},
		{"fenced", fence + "json\n{\"a\": 1}\n" + fence, "{\"a\": 1}"},
		{"leading prose before fence", "All patches reviewed.\n\n" + fence + "json\n{\"a\": 1}\n" + fence, "{\"a\": 1}"},
		{"leading and trailing prose", "Done.\n" + fence + "json\n{\"a\": 1}\n" + fence + "\nHope this helps!", "{\"a\": 1}"},
		{"fence inside JSON string not mistaken for opening", "{\"desc\": \"use \n" + fence + " fences\"}", "{\"desc\": \"use \n" + fence + " fences\"}"},
		{"prose without any fence untouched", "not json at all", "not json at all"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripMarkdownCodeBlocks(tt.content); got != tt.want {
				t.Errorf("StripMarkdownCodeBlocks(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}
