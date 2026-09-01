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
		"escapePipes":         escapePipes,
		"escapeCell":          escapeCell,
		"severityEmoji":       severityEmoji,
		"authorizationStatus": authorizationStatus,
		"guidanceStatus":      guidanceStatus,
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

// guidanceStatus is the status column for the user guidance table. External
// entries (extra_guidance, caller-supplied) are neither sourced from nor
// verified against a fetched PR/MR - IsAuthorized is asserted by the
// caller, not checked - so they get their own label rather than reusing
// "Authorized", which would otherwise misleadingly imply the same
// verification (and, per the report's own caption, that they were used in
// the analysis - they aren't).
func guidanceStatus(g types.UserGuidance) string {
	if g.IsExternal {
		return "🌐 External (not analyzed)"
	}
	return authorizationStatus(g.IsAuthorized)
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

// StripMarkdownCodeBlocks extracts the JSON payload from analysis output.
// Exported so validation and rendering strip identically.
//
// Models reliably wrap JSON in markdown fences and, despite instructions,
// sometimes prefix a line of prose before the fence (observed repeatedly in
// live runs - instruction-following is probabilistic; this parser is the
// deterministic guarantee). Leading prose is tolerated ONLY when a fence
// appears before the first '{', so a fence inside a JSON string can never
// be mistaken for the opening marker. The JSON itself stays strictly
// validated by the caller.
func StripMarkdownCodeBlocks(content string) string {
	trimmed := strings.TrimSpace(content)

	if !strings.HasPrefix(trimmed, "```") {
		fence := strings.Index(trimmed, "\n```")
		brace := strings.Index(trimmed, "{")
		if fence == -1 || (brace != -1 && brace < fence) {
			// No leading fence before the JSON starts: return as-is.
			return trimmed
		}
		// Drop the prose preamble; continue with the fence at line start.
		trimmed = trimmed[fence+1:]
	}

	// Remove opening marker (```json or ``` followed by newline)
	if idx := strings.Index(trimmed, "\n"); idx != -1 {
		trimmed = trimmed[idx+1:]
	}

	// Remove the closing marker and anything after it (trailing prose after
	// the closing fence is discarded along with it).
	if idx := strings.LastIndex(trimmed, "\n```"); idx != -1 {
		trimmed = trimmed[:idx]
	} else {
		trimmed = strings.TrimSuffix(trimmed, "```")
	}

	return strings.TrimSpace(trimmed)
}

// Verdict is the release recommendation computed from the analysis's
// structured risk facts. It is the report's single decision signal.
type Verdict string

const (
	VerdictRelease        Verdict = "release"
	VerdictReview         Verdict = "review"
	VerdictNotRecommended Verdict = "not_recommended"
)

// DefaultBlockOn is the severity at which a concern blocks the release when
// the caller doesn't set ReportConfig.BlockOn.
const DefaultBlockOn = "critical"

// severityRank orders severities most-severe-first. An unrecognized
// severity is treated as rank 0 (critical) by ComputeVerdict - fail toward
// caution; validation upstream guarantees the enum for the helper's own
// runs, so this only matters for library callers.
var severityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}

func rankOf(severity string) int {
	if r, ok := severityRank[severity]; ok {
		return r
	}
	return severityRank["critical"]
}

// ComputeVerdict derives the release verdict from the analysis's concerns
// and its critical ("complete before release") action items, under the
// blockOn policy - the severity at or above which a concern blocks the
// release outright. Concerns exactly one rank below blockOn require manual
// review, and outstanding critical action items escalate an otherwise clean
// release to manual review. The returned reasons are display-ready lines
// naming exactly what drove the verdict; empty means nothing blocked it.
//
// This is the "computed, not written" half of the report's promise: the
// verdict derives only from schema-validated severities, each attached to a
// human-checkable description, never from analysis prose.
func ComputeVerdict(concerns []RiskConcern, criticalActionItems []string, blockOn string) (Verdict, []string) {
	blockRank, ok := severityRank[blockOn]
	if !ok {
		blockRank = severityRank[DefaultBlockOn]
	}

	var blocking, reviewable []string
	for _, c := range concerns {
		switch r := rankOf(c.Severity); {
		case r <= blockRank:
			blocking = append(blocking, severityEmoji(c.Severity)+" "+c.Description)
		case r == blockRank+1:
			reviewable = append(reviewable, severityEmoji(c.Severity)+" "+c.Description)
		}
	}

	if len(blocking) > 0 {
		return VerdictNotRecommended, blocking
	}
	if len(reviewable) > 0 {
		return VerdictReview, reviewable
	}
	if len(criticalActionItems) > 0 {
		reasons := make([]string, 0, len(criticalActionItems))
		for _, item := range criticalActionItems {
			reasons = append(reasons, "📋 Complete before release: "+item)
		}
		return VerdictReview, reasons
	}
	return VerdictRelease, nil
}

func verdictBanner(v Verdict) string {
	switch v {
	case VerdictRelease:
		return "✅ Recommended for release"
	case VerdictReview:
		return "⚠️ **MANUAL REVIEW REQUIRED**"
	default:
		return "🚫 **RELEASE NOT RECOMMENDED**"
	}
}

func severityEmoji(severity string) string {
	switch severity {
	case "high":
		return "⚠️"
	case "medium":
		return "🟡"
	case "low":
		return "🟢"
	default: // critical, or unrecognized (treated as critical)
		return "🔥"
	}
}

// StructuredAnalysis represents the LLM's analysis output in a structured format (v2 schema)
type StructuredAnalysis struct {
	// Model names the model that produced this analysis - the risk-analyst
	// agent states its own identity, and the report footer credits it.
	// Required by the render command's validation; the renderer itself
	// tolerates absence for library callers.
	Model                        string           `json:"model,omitempty"`
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
	LLMResponse   string
	Metadata      *ReportMetadata
	Comparisons   []*types.Comparison
	Documentation []*types.Documentation
	UserGuidance  []types.UserGuidance

	// BlockOn is the severity at or above which a concern blocks the
	// release ("critical", "high", or "medium"); empty means DefaultBlockOn.
	BlockOn        string
	TruncationInfo *TruncationInfo
}

// TruncationInfo summarizes patch truncation applied at fetch time (see
// internal/risk.Truncate), so the report can disclose that the analysis
// didn't see every line of every file. Nil when nothing was truncated.
type TruncationInfo struct {
	TotalFiles     int
	FilesPreserved int
	FilesTruncated int
	TruncatedFiles []string
}

// TemplateData holds all data needed for template rendering
type TemplateData struct {
	Analysis              *StructuredAnalysis
	Metadata              *ReportMetadata
	Comparisons           []*types.Comparison
	Documentation         []*types.Documentation
	ReleaseRecommendation string
	VerdictReasons        []string             // display-ready lines naming what drove the verdict
	AllUserGuidance       []types.UserGuidance // All user guidance for comprehensive reporting
	TruncationInfo        *TruncationInfo
}

// GenerateReport parses LLM response and generates the final report
func GenerateReport(config *ReportConfig) (verdict Verdict, report string, err error) {
	// Strip markdown code blocks if present (LLMs sometimes wrap JSON in ```json ... ```)
	jsonContent := StripMarkdownCodeBlocks(config.LLMResponse)

	// Parse the structured JSON response
	var analysis StructuredAnalysis
	if err := json.Unmarshal([]byte(jsonContent), &analysis); err != nil {
		return "", "", fmt.Errorf("failed to parse JSON response: %w", err)
	}

	// The footer credits the model stated by the analysis itself: the
	// risk-analyst agent is the one whose model matters, and only it
	// knows what it ran as.
	if analysis.Model != "" && config.Metadata != nil {
		config.Metadata.ModelID = analysis.Model
	}

	// Sort user guidance by date (ascending)
	sort.Slice(config.UserGuidance, func(i, j int) bool {
		return config.UserGuidance[i].Date.Before(config.UserGuidance[j].Date)
	})

	// Compute the verdict from the concern severities and the block_on
	// policy - never from analysis prose.
	blockOn := config.BlockOn
	if blockOn == "" {
		blockOn = DefaultBlockOn
	}
	verdict, reasons := ComputeVerdict(analysis.RiskSummary.Concerns, analysis.ActionItems.Critical, blockOn)

	// Create template data
	templateData := &TemplateData{
		Analysis:              &analysis,
		Metadata:              config.Metadata,
		Comparisons:           config.Comparisons,
		Documentation:         config.Documentation,
		ReleaseRecommendation: verdictBanner(verdict),
		VerdictReasons:        reasons,
		AllUserGuidance:       config.UserGuidance,
		TruncationInfo:        config.TruncationInfo,
	}

	// Execute pre-compiled template
	var buf bytes.Buffer
	if err := reportTemplate.Execute(&buf, templateData); err != nil {
		return "", "", fmt.Errorf("failed to execute report template: %w", err)
	}

	return verdict, buf.String(), nil
}
