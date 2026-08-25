// Command soundings is the helper CLI behind the soundings Claude Code skill.
//
// Subcommands:
//
//	fetch  --out <dir> <compare-url> [<compare-url>...]
//	       Fetches commits, diffs, PR/MR metadata, authorized guidance, and
//	       repository documentation for one or more GitHub/GitLab
//	       compare URLs. Writes per-file patches and docs under <dir> and an
//	       index.json describing everything; the agent reads the index first
//	       and opens patch files selectively.
//
//	render --analysis <file> --data <dir> [flags]
//	       Validates the agent's structured analysis JSON (field-level errors
//	       on mismatch) and renders the final markdown report, computing the
//	       recommendation banner from the score and thresholds.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
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

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "fetch":
		err = runFetch(os.Args[2:])
	case "render":
		err = runRender(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "soundings %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `soundings - release confidence helper

USAGE:
  soundings fetch --out <dir> <compare-url> [<compare-url>...]
  soundings render --analysis <file> --data <dir> [--auto-deploy 80] [--review-required 60]
                   [--feedback-url <url>] [--app-interface-mode] [--extra-guidance <file>]
                   [--model-id <id>] [-o <file>]

AUTH:
  GitHub: GITHUB_TOKEN, or 'gh auth login'
  GitLab: GITLAB_TOKEN, or 'glab auth login' (per host, derived from the compare URL)
`)
}

// ---------------------------------------------------------------------------
// fetch
// ---------------------------------------------------------------------------

// Index is the agent-facing summary written to <out>/index.json.
// Patch bodies and doc contents live in separate files so the agent can
// decide what to read in full versus skim.
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

func runFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	outDir := fs.String("out", "", "output directory for index.json, patches/ and docs/ (required)")
	compareURLsFlag := fs.String("compare-urls", "", "comma-separated compare URLs (may also be passed as positional args)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	urls := fs.Args()
	if *compareURLsFlag != "" {
		for _, u := range strings.Split(*compareURLsFlag, ",") {
			urls = append(urls, strings.TrimSpace(u))
		}
	}
	for i, u := range urls {
		urls[i] = stripQueryFragment(u)
	}
	urls = dedupe(urls)
	if len(urls) == 0 {
		return fmt.Errorf("no compare URLs provided")
	}
	if *outDir == "" {
		return fmt.Errorf("--out <dir> is required")
	}
	// Absolute paths in index.json so render works from any directory.
	absOut, err := filepath.Abs(*outDir)
	if err != nil {
		return err
	}
	*outDir = absOut
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}

	providers, err := buildProviders(urls)
	if err != nil {
		return err
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
		return err
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
		patchDir := filepath.Join(*outDir, "patches", slug(c.DiffURL))
		if err := os.MkdirAll(patchDir, 0o755); err != nil {
			return err
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
					return err
				}
				fi.PatchFile = p
			}
			ri.Files = append(ri.Files, fi)
		}
		index.Repos = append(index.Repos, ri)
	}

	// Write documentation contents and index entries.
	for _, d := range docs {
		docDir := filepath.Join(*outDir, "docs", slug(d.Repository.URL))
		if err := os.MkdirAll(docDir, 0o755); err != nil {
			return err
		}
		mainPath := ""
		if d.MainDocFile != "" {
			mainPath = filepath.Join(docDir, "main-"+slug(d.MainDocFile)+".md")
			if err := os.WriteFile(mainPath, []byte(d.MainDocContent), 0o644); err != nil {
				return err
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
				return err
			}
			di.AdditionalDocs = append(di.AdditionalDocs, DocEntry{Name: name, Path: p})
		}
		index.Docs = append(index.Docs, di)
	}

	indexPath := filepath.Join(*outDir, "index.json")
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		return err
	}

	totalFiles := 0
	for _, r := range index.Repos {
		totalFiles += len(r.Files)
	}
	fmt.Printf("Index written to %s (%d repo(s), %d changed file(s), %d guidance item(s), %d doc source(s))\n",
		indexPath, len(index.Repos), totalFiles, len(index.Guidance), len(index.Docs))
	return nil
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

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	analysisPath := fs.String("analysis", "", "path to the structured analysis JSON produced by the agent (required)")
	dataDir := fs.String("data", "", "the fetch output directory containing index.json (required)")
	autoDeploy := fs.Int("auto-deploy", 80, "score at or above which release is recommended")
	reviewRequired := fs.Int("review-required", 60, "score at or above which manual review (instead of no-go) is recommended")
	feedbackURL := fs.String("feedback-url", "", "optional feedback URL embedded in the report")
	appInterfaceMode := fs.Bool("app-interface-mode", false, "enable app-interface report conventions (override-justification banner)")
	extraGuidance := fs.String("extra-guidance", "", "optional JSON file with additional pre-authorized guidance entries")
	modelID := fs.String("model-id", "claude-code", "model identifier for the report footer")
	outFile := fs.String("o", "", "write report to file instead of stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *analysisPath == "" || *dataDir == "" {
		return fmt.Errorf("--analysis and --data are required")
	}
	if *autoDeploy < 0 || *autoDeploy > 100 || *reviewRequired < 0 || *reviewRequired > 100 {
		return fmt.Errorf("--auto-deploy and --review-required must be between 0 and 100")
	}
	if *autoDeploy < *reviewRequired {
		return fmt.Errorf("--auto-deploy (%d) must be >= --review-required (%d)", *autoDeploy, *reviewRequired)
	}

	analysisRaw, err := os.ReadFile(*analysisPath)
	if err != nil {
		return err
	}
	// Strip markdown fences once; validation and rendering must see the
	// exact same bytes so they cannot disagree.
	analysisJSON := report.StripMarkdownCodeBlocks(string(analysisRaw))
	if errs := validateAnalysis([]byte(analysisJSON)); len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "analysis JSON failed validation; fix these fields and re-run:")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		return fmt.Errorf("%d validation error(s)", len(errs))
	}

	indexData, err := os.ReadFile(filepath.Join(*dataDir, "index.json"))
	if err != nil {
		return fmt.Errorf("reading fetch index: %w", err)
	}
	var index Index
	if err := json.Unmarshal(indexData, &index); err != nil {
		return fmt.Errorf("parsing fetch index: %w", err)
	}

	comparisons, documentation, err := reconstruct(&index)
	if err != nil {
		return err
	}

	guidance := index.Guidance
	if *extraGuidance != "" {
		extra, err := readExtraGuidance(*extraGuidance)
		if err != nil {
			return err
		}
		guidance = append(guidance, extra...)
	}

	_, out, err := report.GenerateReport(&report.ReportConfig{
		LLMResponse:             analysisJSON,
		Metadata:                &report.ReportMetadata{ModelID: *modelID, GenerationTime: time.Now().UTC()},
		Comparisons:             comparisons,
		Documentation:           documentation,
		UserGuidance:            guidance,
		AutoDeployThreshold:     *autoDeploy,
		ReviewRequiredThreshold: *reviewRequired,
		AppInterfaceMode:        *appInterfaceMode,
		FeedbackURL:             *feedbackURL,
	})
	if err != nil {
		return err
	}

	if *outFile != "" {
		return os.WriteFile(*outFile, []byte(out), 0o644)
	}
	fmt.Print(out)
	return nil
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

func readExtraGuidance(path string) ([]types.UserGuidance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []extraGuidanceEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parsing extra guidance: %w (expected a JSON array of {content, author, date, comment_url})", err)
	}
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
