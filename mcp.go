package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runMCP serves the fetch and render operations as MCP tools over stdio.
//
// Design rule: this function does nothing but register handlers and wait.
// No credential lookup, no network access, no filesystem writes happen until
// a tool is actually called — the server is inert at rest, so its eager
// per-session start (the harness offers no lazy start for local stdio
// servers) costs a dormant process and nothing else.
func runMCP() error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "soundings",
		Title:   "Soundings release confidence helper",
		Version: pluginVersion,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name: "fetch",
		Description: "Fetch release data for one or more GitHub/GitLab compare URLs: " +
			"commits, diffs, PR/MR metadata, authorized reviewer guidance, and repository " +
			"documentation (SSRF-hardened). Auth is resolved per platform and per host " +
			"(GITHUB_TOKEN / gh auth token, GITLAB_TOKEN / glab auth token). Patches and " +
			"docs are written to disk for the read-only assessment stage; the result " +
			"contains only counts and paths, never fetched content.",
	}, fetchTool)

	mcp.AddTool(server, &mcp.Tool{
		Name: "render",
		Description: "Validate a structured release analysis JSON and render the final " +
			"markdown report. The analysis must contain the model field stated by the " +
			"assessing agent; validation failures are returned as field-level errors so " +
			"the assessment can be corrected and re-run. The recommendation banner is " +
			"computed from the score and thresholds, never from analysis prose.",
	}, renderTool)

	return server.Run(context.Background(), &mcp.StdioTransport{})
}

type fetchToolInput struct {
	CompareURLs []string `json:"compare_urls" jsonschema:"GitHub/GitLab compare URLs to analyze together; mixed platforms and hosts allowed"`
	OutDir      string   `json:"out_dir,omitempty" jsonschema:"directory for index.json, patches/ and docs/; a temporary directory is created when omitted. A custom directory must keep a path component starting with soundings- or the read-confinement hook will deny the assessment stage's reads"`
}

func fetchTool(ctx context.Context, req *mcp.CallToolRequest, in fetchToolInput) (*mcp.CallToolResult, *FetchSummary, error) {
	if len(in.CompareURLs) == 0 {
		return nil, nil, errors.New("compare_urls is required: pass at least one GitHub/GitLab compare URL")
	}
	outDir := in.OutDir
	if outDir == "" {
		d, err := os.MkdirTemp("", "soundings-")
		if err != nil {
			return nil, nil, err
		}
		outDir = d
	}
	summary, err := doFetch(in.CompareURLs, outDir)
	if err != nil {
		return nil, nil, err
	}
	return nil, summary, nil
}

type renderToolInput struct {
	AnalysisJSON     string               `json:"analysis_json" jsonschema:"the structured analysis JSON produced by the assessment stage, passed verbatim"`
	DataDir          string               `json:"data_dir" jsonschema:"the fetch output directory containing index.json"`
	AutoDeploy       *int                 `json:"auto_deploy,omitempty" jsonschema:"score at or above which release is recommended (default 80)"`
	ReviewRequired   *int                 `json:"review_required,omitempty" jsonschema:"score at or above which manual review (instead of no-go) is recommended (default 60)"`
	ExtraGuidance    []extraGuidanceEntry `json:"extra_guidance,omitempty" jsonschema:"caller-vouched pre-authorized guidance entries to include in the report"`
}

func renderTool(ctx context.Context, req *mcp.CallToolRequest, in renderToolInput) (*mcp.CallToolResult, *RenderResult, error) {
	if strings.TrimSpace(in.AnalysisJSON) == "" || in.DataDir == "" {
		return nil, nil, errors.New("analysis_json and data_dir are required")
	}
	opts := renderOpts{
		AutoDeploy:     80,
		ReviewRequired: 60,
		ExtraGuidance:  in.ExtraGuidance,
	}
	if in.AutoDeploy != nil {
		opts.AutoDeploy = *in.AutoDeploy
	}
	if in.ReviewRequired != nil {
		opts.ReviewRequired = *in.ReviewRequired
	}

	result, validationErrs, err := doRender(in.AnalysisJSON, in.DataDir, opts)
	if err != nil {
		return nil, nil, err
	}
	if len(validationErrs) > 0 {
		return nil, nil, fmt.Errorf("analysis JSON failed validation; correct these fields and re-run the assessment:\n  - %s",
			strings.Join(validationErrs, "\n  - "))
	}
	return nil, result, nil
}
