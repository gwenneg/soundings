package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runMCP serves the fetch and render operations as MCP tools over stdio.
//
// Design rule: no credential lookup and no network access until a tool is
// actually called — the server is inert at rest, so its eager per-session
// start (the harness offers no lazy start for local stdio servers) costs a
// dormant process and nothing else. The one exception is a local registry
// prune at startup, so an abandoned run's data and fence are retired when
// soundings next starts rather than only when a later fetch happens to run.
func runMCP() error {
	if entries, err := entriesDir(); err == nil {
		pruneEntries(entries)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:  "soundings",
		Title: "Soundings release readiness helper",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "fetch",
		Description: "Fetch release data for one or more GitHub/GitLab compare URLs: " +
			"commits, diffs, PR/MR metadata, authorized reviewer guidance, and repository " +
			"documentation (SSRF-hardened). Auth is resolved per platform and per host " +
			"(GITHUB_TOKEN / gh auth token, GITLAB_TOKEN / glab auth token). Patches and " +
			"docs are written to a helper-owned temporary directory for the read-only " +
			"analysis stage (deleted when the run's render succeeds); the result " +
			"contains only counts and paths, never fetched content.",
	}, fetchTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "render",
		Description: "Validate a structured release analysis JSON and render the final " +
			"markdown report. The analysis must contain the model field stated by the " +
			"risk-analyst agent; validation failures are returned as field-level errors so " +
			"the analysis can be corrected and re-run. The release verdict is computed " +
			"from the concern severities and the block_on policy, never from analysis prose.",
	}, renderTool)

	return server.Run(context.Background(), &mcp.StdioTransport{})
}

type fetchToolInput struct {
	CompareURLs []string `json:"compare_urls" jsonschema:"GitHub/GitLab compare URLs to analyze together; mixed platforms and hosts allowed"`
}

func fetchTool(ctx context.Context, req *mcp.CallToolRequest, in fetchToolInput) (*mcp.CallToolResult, *FetchSummary, error) {
	if len(in.CompareURLs) == 0 {
		return nil, nil, errors.New("compare_urls is required: pass at least one GitHub/GitLab compare URL")
	}
	outDir, err := os.MkdirTemp("", "soundings-")
	if err != nil {
		return nil, nil, err
	}
	// Register before any content is written: the fence then covers the
	// directory for its whole life, and a crash mid-fetch leaves an entry
	// the TTL prune will find. Fatal on failure - an unregistered fetch
	// would have every read of the isolated stage denied with no clue why.
	if err := registerDir(outDir); err != nil {
		os.RemoveAll(outDir)
		return nil, nil, fmt.Errorf("registering fetch directory for the read-confinement hook: %w", err)
	}
	summary, err := doFetch(in.CompareURLs, outDir)
	if err != nil {
		// A failed fetch must not leave partially fetched, unregistered
		// content outside the keep-out.
		releaseDir(outDir)
		return nil, nil, err
	}
	return nil, summary, nil
}

type renderToolInput struct {
	AnalysisJSON  string               `json:"analysis_json" jsonschema:"the structured analysis JSON produced by the risk-analyst stage, passed verbatim"`
	DataDir       string               `json:"data_dir" jsonschema:"the fetch output directory containing index.json"`
	BlockOn       string               `json:"block_on,omitempty" jsonschema:"severity at or above which a concern blocks the release: critical (default), high, or medium; concerns one level below produce a manual-review verdict"`
	ExtraGuidance []extraGuidanceEntry `json:"extra_guidance,omitempty" jsonschema:"caller-supplied guidance entries; the analyze skill relays those with is_authorized true to the risk-analyst as guidance, and the report lists all of them as external"`
	ReportPath    string               `json:"report_path" jsonschema:"absolute path ending in .md the rendered report is written to; an existing file is only overwritten if it is a previously generated soundings report. Required: the data_dir is deleted after a successful render, so this file is how the report outlives the run"`
}

func renderTool(ctx context.Context, req *mcp.CallToolRequest, in renderToolInput) (*mcp.CallToolResult, *RenderResult, error) {
	if strings.TrimSpace(in.AnalysisJSON) == "" || in.DataDir == "" || in.ReportPath == "" {
		return nil, nil, errors.New("analysis_json, data_dir and report_path are required")
	}
	opts := renderOpts{
		BlockOn:       in.BlockOn,
		ExtraGuidance: in.ExtraGuidance,
		ReportPath:    in.ReportPath,
	}

	result, validationErrs, err := doRender(in.AnalysisJSON, in.DataDir, opts)
	if err != nil {
		return nil, nil, err
	}
	if len(validationErrs) > 0 {
		return nil, nil, fmt.Errorf("analysis JSON failed validation; the analysis as received is saved at %s; correct these fields and re-run the analysis:\n  - %s",
			filepath.Join(in.DataDir, "analysis.json"),
			strings.Join(validationErrs, "\n  - "))
	}
	return nil, result, nil
}
