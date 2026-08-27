// Command soundings is the MCP server behind the soundings Claude Code
// plugin. It exposes two tools over stdio:
//
//	fetch   Fetches commits, diffs, PR/MR metadata, authorized guidance, and
//	        repository documentation for one or more GitHub/GitLab compare
//	        URLs. Writes per-file patches and docs to disk and returns a
//	        summary of counts and paths; the read-only assessment stage
//	        opens the content selectively.
//
//	render  Validates the structured analysis JSON (field-level errors on
//	        mismatch) and renders the final markdown report, computing the
//	        recommendation banner from the score and thresholds.
//
// With the single argument "hook" it instead runs as a Claude Code
// PreToolUse hook that confines the assess agent's Read tool to the fetch
// output directory (see hook.go).
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/gwenneg/soundings/internal/config"
	"github.com/gwenneg/soundings/internal/git/github"
	"github.com/gwenneg/soundings/internal/git/gitlab"
	"github.com/gwenneg/soundings/internal/git/types"
	"github.com/gwenneg/soundings/internal/report"
	"github.com/gwenneg/soundings/internal/risk"
)

// pluginVersion mirrors .claude-plugin/plugin.json; bump both together.
const pluginVersion = "0.2.2"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "hook" {
		if err := runHook(os.Stdin, os.Stdout); err != nil {
			// Exit 1 is a non-blocking hook error: surfaced, but it does
			// not silently allow or deny the tool call.
			fmt.Fprintf(os.Stderr, "soundings hook: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 {
		fmt.Fprintf(os.Stderr, "soundings %s is an MCP server; run it with no arguments (stdio transport), or with 'hook' as the PreToolUse confinement hook\n", pluginVersion)
		os.Exit(2)
	}
	err := runMCP()
	// stdin closing is the normal way a stdio MCP session ends
	if err != nil && strings.Contains(err.Error(), "EOF") {
		err = nil
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "soundings: %v\n", err)
		os.Exit(1)
	}
}

type Index struct {
	CompareURLs []string             `json:"compare_urls"`
	Repos       []RepoIndex          `json:"repos"`
	Guidance    []types.UserGuidance `json:"guidance"`
	Docs        []DocIndex           `json:"docs"`
}

type RepoIndex struct {
	Platform string                `json:"platform"`
	RepoURL  string                `json:"repo_url"`
	DiffURL  string                `json:"diff_url"`
	Stats    types.ComparisonStats `json:"stats"`
	Commits  []types.Commit        `json:"commits"`
	Files    []FileIndex           `json:"files"`

	// FilesMayBeTruncated: the platform API capped the file list (GitHub
	// compare returns at most 300 files) - stats and patches are partial.
	FilesMayBeTruncated bool `json:"files_may_be_truncated,omitempty"`
}

type FileIndex struct {
	Filename         string `json:"filename"`
	Status           string `json:"status"`
	PreviousFilename string `json:"previous_filename,omitempty"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Changes          int    `json:"changes"`
	RiskTier         string `json:"risk_tier"`
	PatchLines       int    `json:"patch_lines"`
	PatchFile        string `json:"patch_file,omitempty"`
}

type DocIndex struct {
	Platform       string            `json:"platform"`
	RepoURL        string            `json:"repo_url"`
	DefaultBranch  string            `json:"default_branch"`
	MainDocFile    string            `json:"main_doc_file"`
	MainDocPath    string            `json:"main_doc_path"`
	AdditionalDocs []DocEntry        `json:"additional_docs,omitempty"`
	FailedDocs     map[string]string `json:"failed_docs,omitempty"`

	// FetchError: the main documentation lookup failed for a reason other
	// than the file not existing - docs are unavailable, not absent.
	FetchError string `json:"fetch_error,omitempty"`
}

type DocEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type FetchSummary struct {
	IndexPath     string        `json:"index_path" jsonschema:"path to the index.json describing the fetched data"`
	Repos         []RepoSummary `json:"repos"`
	GuidanceCount int           `json:"guidance_count" jsonschema:"number of authorized-or-not guidance entries collected"`
	Docs          []DocSummary  `json:"docs,omitempty"`
}

type RepoSummary struct {
	Platform            string `json:"platform"`
	RepoURL             string `json:"repo_url"`
	DiffURL             string `json:"diff_url"`
	Commits             int    `json:"commits"`
	Files               int    `json:"files"`
	FilesMayBeTruncated bool   `json:"files_may_be_truncated,omitempty" jsonschema:"true when the platform API capped the file list - the diff is partial"`
}

type DocSummary struct {
	RepoURL    string `json:"repo_url"`
	Found      bool   `json:"found"`
	FetchError string `json:"fetch_error,omitempty" jsonschema:"set when documentation was unavailable (auth/network), as opposed to absent"`
}

// doFetch is the core of the fetch operation, shared by the CLI and the MCP
// server.
func doFetch(urls []string, outDir string) (*FetchSummary, error) {
	for i, u := range urls {
		urls[i] = stripQueryFragment(u)
	}
	urls = dedupe(urls)
	if len(urls) == 0 {
		return nil, fmt.Errorf("no compare URLs provided")
	}
	// Absolute paths in index.json so render works from any directory.
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return nil, err
	}
	outDir = absOut
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	providers, err := buildProviders(urls)
	if err != nil {
		return nil, err
	}

	// Fetch all URLs in parallel (mirrors the original ReleaseAnalyzer).
	g, gCtx := errgroup.WithContext(context.Background())
	var mu sync.Mutex
	var comparisons []*types.Comparison
	var guidance []types.UserGuidance
	var docs []*types.Documentation

	for _, u := range urls {
		g.Go(func() error {
			provider, ok := providers[providerKey(u)]
			if !ok {
				return fmt.Errorf("unsupported compare URL: %s", u)
			}
			comparison, ug, doc, err := provider.FetchReleaseData(gCtx, u)
			if err != nil {
				return fmt.Errorf("failed to fetch %s: %w", u, err)
			}
			if comparison == nil {
				return fmt.Errorf("no comparison data returned for %s", u)
			}
			mu.Lock()
			defer mu.Unlock()
			comparisons = append(comparisons, comparison)
			guidance = append(guidance, ug...)
			if doc != nil && (doc.MainDocFile != "" || doc.FetchError != "") {
				docs = append(docs, doc)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	index := Index{CompareURLs: urls, Guidance: guidance}
	if index.Guidance == nil {
		index.Guidance = []types.UserGuidance{}
	}

	// Write patches and build the per-repo index.
	for _, c := range comparisons {
		// Key the patch directory by the full compare URL, not just the
		// repo: two ranges of the same repo in one run must not overwrite
		// each other's patch files.
		patchDir := filepath.Join(outDir, "patches", slug(c.DiffURL))
		if err := os.MkdirAll(patchDir, 0o755); err != nil {
			return nil, err
		}
		ri := RepoIndex{Platform: c.Platform, RepoURL: c.RepoURL, DiffURL: c.DiffURL, Stats: c.Stats, Commits: c.Commits, FilesMayBeTruncated: c.FilesMayBeTruncated}
		for i, f := range c.Files {
			fi := FileIndex{
				Filename:         f.Filename,
				Status:           f.Status,
				PreviousFilename: f.PreviousFilename,
				Additions:        f.Additions,
				Deletions:        f.Deletions,
				Changes:          f.Changes,
				RiskTier:         risk.ClassifyFile(f.Filename),
				PatchLines:       countLines(f.Patch),
			}
			if f.Patch != "" {
				p := filepath.Join(patchDir, fmt.Sprintf("%03d-%s.patch", i, slug(f.Filename)))
				if err := os.WriteFile(p, []byte(f.Patch), 0o644); err != nil {
					return nil, err
				}
				fi.PatchFile = p
			}
			ri.Files = append(ri.Files, fi)
		}
		index.Repos = append(index.Repos, ri)
	}

	// Write documentation contents and index entries.
	for _, d := range docs {
		docDir := filepath.Join(outDir, "docs", slug(d.Repository.URL))
		if err := os.MkdirAll(docDir, 0o755); err != nil {
			return nil, err
		}
		mainPath := ""
		if d.MainDocFile != "" {
			mainPath = filepath.Join(docDir, "main-"+slug(d.MainDocFile)+".md")
			if err := os.WriteFile(mainPath, []byte(d.MainDocContent), 0o644); err != nil {
				return nil, err
			}
		}
		di := DocIndex{
			Platform:      d.Repository.Platform,
			RepoURL:       d.Repository.URL,
			DefaultBranch: d.Repository.DefaultBranch,
			MainDocFile:   d.MainDocFile,
			MainDocPath:   mainPath,
			FailedDocs:    d.FailedAdditionalDocs,
			FetchError:    d.FetchError,
		}
		for _, name := range d.AdditionalDocsOrder {
			content, ok := d.AdditionalDocsContent[name]
			if !ok {
				continue
			}
			p := filepath.Join(docDir, "additional-"+slug(name)+".md")
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				return nil, err
			}
			di.AdditionalDocs = append(di.AdditionalDocs, DocEntry{Name: name, Path: p})
		}
		index.Docs = append(index.Docs, di)
	}

	indexPath := filepath.Join(outDir, "index.json")
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		return nil, err
	}

	summary := &FetchSummary{IndexPath: indexPath, GuidanceCount: len(index.Guidance)}
	for _, r := range index.Repos {
		summary.Repos = append(summary.Repos, RepoSummary{
			Platform:            r.Platform,
			RepoURL:             r.RepoURL,
			DiffURL:             r.DiffURL,
			Commits:             len(r.Commits),
			Files:               len(r.Files),
			FilesMayBeTruncated: r.FilesMayBeTruncated,
		})
	}
	for _, d := range index.Docs {
		summary.Docs = append(summary.Docs, DocSummary{
			RepoURL:    d.RepoURL,
			Found:      d.MainDocFile != "",
			FetchError: d.FetchError,
		})
	}
	return summary, nil
}

// buildProviders resolves auth and constructs one GitProvider per platform/host
// needed by the given URLs. GitHub URLs share one client; GitLab gets one
// client per host, each with a token resolved for exactly that host so a
// token for host X is never sent to host Y.
func buildProviders(urls []string) (map[string]types.GitProvider, error) {
	providers := make(map[string]types.GitProvider)
	gitlabHosts := make(map[string]bool)
	for _, u := range urls {
		if k := providerKey(u); strings.HasPrefix(k, "gitlab:") {
			gitlabHosts[strings.TrimPrefix(k, "gitlab:")] = true
		}
	}
	for _, u := range urls {
		key := providerKey(u)
		if key == "" {
			return nil, fmt.Errorf("unsupported compare URL (expected GitHub .../compare/a...b or GitLab .../-/compare/a...b): %s", u)
		}
		if _, ok := providers[key]; ok {
			continue
		}
		if key == "github" {
			token, err := githubToken()
			if err != nil {
				return nil, err
			}
			cfg := &config.Config{GitHubToken: token}
			client, err := github.NewClient(cfg)
			if err != nil {
				return nil, err
			}
			providers[key] = github.NewFetcher(client, cfg)
			continue
		}
		host := strings.TrimPrefix(key, "gitlab:")
		token, err := gitlabToken(host, len(gitlabHosts) == 1)
		if err != nil {
			return nil, err
		}
		cfg := &config.Config{GitLabToken: token, GitLabBaseURL: "https://" + host}
		client, err := gitlab.NewClient(cfg)
		if err != nil {
			return nil, err
		}
		providers[key] = gitlab.NewFetcher(client, cfg)
	}
	return providers, nil
}

var (
	githubCompareRe = regexp.MustCompile(`^https?://github\.com/[^/]+/[^/]+/compare/.+\.\.\..+$`)
	gitlabCompareRe = regexp.MustCompile(`^https?://([^/]+)/.+/-/compare/.+\.\.\..+`)
)

// providerKey classifies a compare URL: "github", "gitlab:<host>", or "".
func providerKey(u string) string {
	if githubCompareRe.MatchString(u) {
		return "github"
	}
	if m := gitlabCompareRe.FindStringSubmatch(u); m != nil {
		return "gitlab:" + strings.ToLower(m[1])
	}
	return ""
}

func githubToken() (string, error) {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t, nil
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err == nil {
		if t := strings.TrimSpace(string(out)); t != "" {
			return t, nil
		}
	}
	return "", fmt.Errorf("GitHub authentication unavailable: set GITHUB_TOKEN or run 'gh auth login'")
}

// gitlabToken resolves the token for one GitLab host. The flat GITLAB_TOKEN
// env var is honored only when the run touches a single GitLab host - in a
// mixed-host run it cannot be attributed to a host, and sending it to every
// host would leak it, so per-host glab auth is required instead.
func gitlabToken(host string, soleHost bool) (string, error) {
	if t := os.Getenv("GITLAB_TOKEN"); t != "" {
		if soleHost {
			return t, nil
		}
	}
	out, err := exec.Command("glab", "auth", "token", "--hostname", host).Output()
	if err == nil {
		if t := strings.TrimSpace(string(out)); t != "" {
			return t, nil
		}
	}
	if os.Getenv("GITLAB_TOKEN") != "" && !soleHost {
		return "", fmt.Errorf("GitLab authentication unavailable for %s: this run spans multiple GitLab hosts, so the flat GITLAB_TOKEN env var is not used (it cannot be attributed to one host) - run 'glab auth login --hostname %s'", host, host)
	}
	return "", fmt.Errorf("GitLab authentication unavailable for %s: set GITLAB_TOKEN or run 'glab auth login --hostname %s'", host, host)
}

func dedupe(in []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

var slugRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// slug turns a URL or path into a single safe filename component, capped so
// deep source paths cannot exceed filesystem name limits; truncated slugs
// get a short hash suffix to stay unique.
func slug(s string) string {
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	out := strings.Trim(slugRe.ReplaceAllString(s, "_"), "_")
	const maxLen = 120
	if len(out) > maxLen {
		sum := sha256.Sum256([]byte(out))
		out = out[:maxLen] + "-" + hex.EncodeToString(sum[:4])
	}
	return out
}

// stripQueryFragment removes ?query and #fragment from a URL, so UI-copied
// compare links (e.g. ...?expand=1) parse the same as clean ones.
func stripQueryFragment(u string) string {
	if i := strings.IndexAny(u, "?#"); i != -1 {
		return u[:i]
	}
	return u
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// ---------------------------------------------------------------------------
// render
// ---------------------------------------------------------------------------

type renderOpts struct {
	AutoDeploy       int
	ReviewRequired   int
	FeedbackURL      string
	AppInterfaceMode bool
	ExtraGuidance    []extraGuidanceEntry
}

// RenderResult is the successful outcome of a render.
type RenderResult struct {
	Score          int    `json:"score"`
	ReportMarkdown string `json:"report_markdown"`
}

// doRender is the core of the render operation, shared by the CLI and the
// MCP server. It returns (nil, validationErrors, nil) when the analysis
// fails schema validation - the caller relays the field-level errors so the
// assessment can be corrected and re-run.
func doRender(analysisRaw, dataDir string, opts renderOpts) (*RenderResult, []string, error) {
	if opts.AutoDeploy < 0 || opts.AutoDeploy > 100 || opts.ReviewRequired < 0 || opts.ReviewRequired > 100 {
		return nil, nil, fmt.Errorf("auto-deploy and review-required thresholds must be between 0 and 100")
	}
	if opts.AutoDeploy < opts.ReviewRequired {
		return nil, nil, fmt.Errorf("auto-deploy (%d) must be >= review-required (%d)", opts.AutoDeploy, opts.ReviewRequired)
	}

	// Strip fences and redact credentials once, before validation:
	// validation and rendering must see the exact same bytes so they
	// cannot disagree, and a secret must never survive into the report
	// even if redaction damages the JSON (that fails validation instead).
	analysisJSON := report.StripMarkdownCodeBlocks(analysisRaw)
	analysisJSON, _ = report.RedactSecrets(analysisJSON)
	if errs := validateAnalysis([]byte(analysisJSON)); len(errs) > 0 {
		return nil, errs, nil
	}

	indexData, err := os.ReadFile(filepath.Join(dataDir, "index.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("reading fetch index: %w", err)
	}
	var index Index
	if err := json.Unmarshal(indexData, &index); err != nil {
		return nil, nil, fmt.Errorf("parsing fetch index: %w", err)
	}

	comparisons, documentation, err := reconstruct(&index)
	if err != nil {
		return nil, nil, err
	}

	guidance := index.Guidance
	if len(opts.ExtraGuidance) > 0 {
		extra, err := toUserGuidance(opts.ExtraGuidance)
		if err != nil {
			return nil, nil, err
		}
		guidance = append(guidance, extra...)
	}

	score, out, err := report.GenerateReport(&report.ReportConfig{
		LLMResponse:             analysisJSON,
		Metadata:                &report.ReportMetadata{GenerationTime: time.Now().UTC()},
		Comparisons:             comparisons,
		Documentation:           documentation,
		UserGuidance:            guidance,
		AutoDeployThreshold:     opts.AutoDeploy,
		ReviewRequiredThreshold: opts.ReviewRequired,
		AppInterfaceMode:        opts.AppInterfaceMode,
		FeedbackURL:             opts.FeedbackURL,
	})
	if err != nil {
		return nil, nil, err
	}

	return &RenderResult{Score: score, ReportMarkdown: out}, nil, nil
}

// reconstruct rebuilds the renderer's view (comparisons without patch bodies,
// documentation with contents re-read from disk) from the fetch index.
func reconstruct(index *Index) ([]*types.Comparison, []*types.Documentation, error) {
	var comparisons []*types.Comparison
	for _, r := range index.Repos {
		comparisons = append(comparisons, &types.Comparison{
			Platform: r.Platform,
			RepoURL:  r.RepoURL,
			DiffURL:  r.DiffURL,
			Stats:    r.Stats,
			Commits:  r.Commits,
		})
	}

	var documentation []*types.Documentation
	for _, d := range index.Docs {
		if d.MainDocFile == "" {
			// Docs were unavailable (fetch error), not absent - nothing to render.
			continue
		}
		mainContent, err := os.ReadFile(d.MainDocPath)
		if err != nil {
			return nil, nil, fmt.Errorf("reading doc %s: %w", d.MainDocPath, err)
		}
		doc := &types.Documentation{
			MainDocFile:           d.MainDocFile,
			MainDocContent:        string(mainContent),
			AdditionalDocsContent: map[string]string{},
			FailedAdditionalDocs:  d.FailedDocs,
			Repository: types.Repository{
				Platform:      d.Platform,
				URL:           d.RepoURL,
				DefaultBranch: d.DefaultBranch,
			},
		}
		for _, a := range d.AdditionalDocs {
			content, err := os.ReadFile(a.Path)
			if err != nil {
				return nil, nil, fmt.Errorf("reading doc %s: %w", a.Path, err)
			}
			doc.AdditionalDocsContent[a.Name] = string(content)
			doc.AdditionalDocsOrder = append(doc.AdditionalDocsOrder, a.Name)
		}
		documentation = append(documentation, doc)
	}
	return comparisons, documentation, nil
}

// extraGuidanceEntry is the caller-facing shape for --extra-guidance files.
// All entries are treated as pre-authorized: the caller vouches for them.
type extraGuidanceEntry struct {
	Content    string `json:"content"`
	Author     string `json:"author"`
	Date       string `json:"date"`
	CommentURL string `json:"comment_url"`
}

// toUserGuidance converts caller-supplied guidance entries; all are treated
// as pre-authorized (the caller vouches for them).
func toUserGuidance(entries []extraGuidanceEntry) ([]types.UserGuidance, error) {
	out := make([]types.UserGuidance, 0, len(entries))
	for i, e := range entries {
		if strings.TrimSpace(e.Content) == "" {
			return nil, fmt.Errorf("extra guidance entry %d: content is required", i)
		}
		g := types.UserGuidance{
			Content:      e.Content,
			Author:       e.Author,
			CommentURL:   e.CommentURL,
			IsAuthorized: true,
		}
		if e.Date != "" {
			for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02"} {
				if t, err := time.Parse(layout, e.Date); err == nil {
					g.Date = t
					break
				}
			}
			// An unparseable date is left zero rather than failing the render.
		}
		out = append(out, g)
	}
	return out, nil
}

var validSeverities = map[string]bool{"critical": true, "high": true, "medium": true, "low": true}

// validateAnalysis checks the agent's structured output against the report
// schema, returning one message per problem so the agent can fix its output
// precisely instead of guessing.
func validateAnalysis(data []byte) []string {
	var errs []string
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var a report.StructuredAnalysis
	if err := dec.Decode(&a); err != nil {
		return []string{fmt.Sprintf("not valid JSON matching the schema: %v", err)}
	}
	if dec.More() {
		return []string{"trailing content after the JSON object: the file must contain exactly one JSON object and nothing else"}
	}
	if strings.TrimSpace(a.Model) == "" {
		errs = append(errs, "model: required - state the exact model identifier that produced this analysis (the assess agent's own identity)")
	}
	if a.Score < 0 || a.Score > 100 {
		errs = append(errs, fmt.Sprintf("score: must be 0-100, got %d", a.Score))
	}
	if strings.TrimSpace(a.Summary) == "" {
		errs = append(errs, "summary: required, must be a non-empty one-line summary")
	}
	for i, c := range a.RiskSummary.Concerns {
		if !validSeverities[c.Severity] {
			errs = append(errs, fmt.Sprintf("risk_summary.concerns[%d].severity: must be one of critical|high|medium|low (lowercase), got %q", i, c.Severity))
		}
		if strings.TrimSpace(c.Description) == "" {
			errs = append(errs, fmt.Sprintf("risk_summary.concerns[%d].description: required", i))
		}
	}
	if strings.TrimSpace(a.DocumentationQuality) == "" {
		errs = append(errs, "documentation_quality: required (assess documentation completeness; say so if none was found)")
	}
	return errs
}
