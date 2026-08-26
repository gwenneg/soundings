package report

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/gwenneg/soundings/internal/git/types"
)

//go:embed report_template.md
var reportTemplateText string

var reportTemplate *template.Template

func init() {
	reportTemplate = template.Must(
		template.New("report").Funcs(templateFuncs()).Parse(reportTemplateText),
	)
}

// templateFuncs returns all custom template functions
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"hasPrefix":           strings.HasPrefix,
		"contains":            strings.Contains,
		"escapePipes":         escapePipes,
		"escapeCell":          escapeCell,
		"authorizationStatus": authorizationStatus,
		"prLink":              prLink,
		"formatAuthor":        formatAuthor,
		"docURL":              docURL,
		"commitLink":          commitLink,
		"formatDate":          formatDate,
		"docFileInfo":         docFileInfo,
	}
}

// Template helper functions

func escapePipes(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

// escapeCell makes arbitrary user text safe inside a one-row markdown table
// cell: pipes are escaped and newlines collapsed to <br> so multi-line
// guidance cannot break the table or inject rows.
func escapeCell(s string) string {
	s = escapePipes(s)
	s = strings.ReplaceAll(s, "\r\n", "<br>")
	s = strings.ReplaceAll(s, "\n", "<br>")
	s = strings.ReplaceAll(s, "\r", "<br>")
	return s
}

func authorizationStatus(isAuthorized bool) string {
	if isAuthorized {
		return "✅ Authorized"
	}
	return "❌ Unauthorized"
}

func prLink(prNumber int64, repoURL, platform string) string {
	if prNumber <= 0 {
		return "N/A"
	}

	// GitLab uses /-/merge_requests/, GitHub uses /pull/
	if platform == "github" {
		return fmt.Sprintf("[#%d](%s/pull/%d)", prNumber, repoURL, prNumber)
	}
	return fmt.Sprintf("[!%d](%s/-/merge_requests/%d)", prNumber, repoURL, prNumber)
}

func formatAuthor(author, commentURL string) string {
	if strings.HasPrefix(commentURL, "https://github.com/") {
		return fmt.Sprintf("[@%s](https://github.com/%s)", author, author)
	}
	return "@" + author
}

func docURL(filename, repoURL, branch, platform string) string {
	if strings.HasPrefix(filename, "http") {
		return filename
	}
	// GitLab's canonical file path uses the /-/ scope; the legacy unscoped
	// form is not redirected for nested subgroups.
	if platform == "gitlab" {
		return fmt.Sprintf("%s/-/blob/%s/%s", repoURL, branch, filename)
	}
	return fmt.Sprintf("%s/blob/%s/%s", repoURL, branch, filename)
}

func commitLink(shortSHA, fullSHA, repoURL, platform string) string {
	if platform == "gitlab" {
		return fmt.Sprintf("[%s](%s/-/commit/%s)", shortSHA, repoURL, fullSHA)
	}
	return fmt.Sprintf("[%s](%s/commit/%s)", shortSHA, repoURL, fullSHA)
}

func formatDate(t time.Time) string {
	return t.Format("2006-01-02 15:04")
}

func docFileInfo(filename, repoURL, branch, platform, content string) string {
	url := docURL(filename, repoURL, branch, platform)
	return fmt.Sprintf("- %s - %d chars", url, len(content))
}

// StripMarkdownCodeBlocks removes markdown code block markers from analysis
// output. Exported so validation and rendering strip identically.
// Handles both ```json and ``` style code blocks
func StripMarkdownCodeBlocks(content string) string {
	trimmed := strings.TrimSpace(content)

	// Return as-is if not wrapped in code blocks
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	// Remove opening marker (```json or ``` followed by newline)
	if idx := strings.Index(trimmed, "\n"); idx != -1 {
		trimmed = trimmed[idx+1:]
	}

	// Remove closing marker
	trimmed = strings.TrimSuffix(trimmed, "```")

	return strings.TrimSpace(trimmed)
}

func getReleaseRecommendation(score, autoDeployThreshold, reviewRequiredThreshold int) string {
	if score >= autoDeployThreshold {
		return "✅ Recommended for release"
	} else if score >= reviewRequiredThreshold {
		return "⚠️ **MANUAL REVIEW REQUIRED**"
	} else {
		return "🚫 **RELEASE NOT RECOMMENDED**"
	}
}

// StructuredAnalysis represents the LLM's analysis output in a structured format (v2 schema)
type StructuredAnalysis struct {
	// Model names the model that produced this analysis - the assessing
	// agent states its own identity, and the report footer credits it.
	// Required by the render command's validation; the renderer itself
	// tolerates absence for library callers.
	Model                        string           `json:"model,omitempty"`
	Score                        int              `json:"score"`
	Summary                      string           `json:"summary"`
	RiskSummary                  RiskSummary      `json:"risk_summary"`
	ActionItems                  ActionItems      `json:"action_items"`
	TechnicalDetails             TechnicalDetails `json:"technical_details"`
	DocumentationQuality         string           `json:"documentation_quality"`
	DocumentationRecommendations string           `json:"documentation_recommendations"`
}

// RiskSummary consolidates all risk-related information
type RiskSummary struct {
	Concerns  []RiskConcern `json:"concerns"`
	Positives []string      `json:"positives"`
}

// RiskConcern represents a single risk with severity
type RiskConcern struct {
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

// ActionItems represents categorized action items
type ActionItems struct {
	Critical  []string `json:"critical"`
	Important []string `json:"important"`
	Followup  []string `json:"followup"`
}

// TechnicalDetails contains findings organized by area
type TechnicalDetails struct {
	Code           []string `json:"code"`
	Infrastructure []string `json:"infrastructure"`
	Dependencies   []string `json:"dependencies"`
}

// ReportMetadata contains metadata for template replacement
type ReportMetadata struct {
	ModelID        string
	GenerationTime time.Time
}

// ReportConfig holds all configuration and data needed for report generation
type ReportConfig struct {
	LLMResponse             string
	Metadata                *ReportMetadata
	Comparisons             []*types.Comparison
	Documentation           []*types.Documentation
	UserGuidance            []types.UserGuidance
	AutoDeployThreshold     int
	ReviewRequiredThreshold int
	AppInterfaceMode        bool
	FeedbackURL             string
}

// TemplateData holds all data needed for template rendering
type TemplateData struct {
	Analysis              *StructuredAnalysis
	Metadata              *ReportMetadata
	Comparisons           []*types.Comparison
	Documentation         []*types.Documentation
	ReleaseRecommendation string
	AllUserGuidance       []types.UserGuidance // All user guidance for comprehensive reporting
	AppInterfaceMode      bool
	FeedbackURL           string
}

// GenerateReport parses LLM response and generates the final report
func GenerateReport(config *ReportConfig) (score int, report string, err error) {
	// Strip markdown code blocks if present (LLMs sometimes wrap JSON in ```json ... ```)
	jsonContent := StripMarkdownCodeBlocks(config.LLMResponse)

	// Parse the structured JSON response
	var analysis StructuredAnalysis
	if err := json.Unmarshal([]byte(jsonContent), &analysis); err != nil {
		return 0, "", fmt.Errorf("failed to parse JSON response: %w", err)
	}

	// The footer credits the model stated by the analysis itself: the
	// assessing agent is the one whose model matters, and only it knows
	// what it ran as.
	if analysis.Model != "" && config.Metadata != nil {
		config.Metadata.ModelID = analysis.Model
	}

	// Sort user guidance by date (ascending)
	sort.Slice(config.UserGuidance, func(i, j int) bool {
		return config.UserGuidance[i].Date.Before(config.UserGuidance[j].Date)
	})

	// Determine release recommendation based on score
	recommendation := getReleaseRecommendation(analysis.Score, config.AutoDeployThreshold, config.ReviewRequiredThreshold)

	// Create template data
	templateData := &TemplateData{
		Analysis:              &analysis,
		Metadata:              config.Metadata,
		Comparisons:           config.Comparisons,
		Documentation:         config.Documentation,
		ReleaseRecommendation: recommendation,
		AllUserGuidance:       config.UserGuidance,
		AppInterfaceMode:      config.AppInterfaceMode,
		FeedbackURL:           config.FeedbackURL,
	}

	// Execute pre-compiled template
	var buf bytes.Buffer
	if err := reportTemplate.Execute(&buf, templateData); err != nil {
		return 0, "", fmt.Errorf("failed to execute report template: %w", err)
	}

	return analysis.Score, buf.String(), nil
}
