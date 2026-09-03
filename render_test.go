package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validAnalysisJSON = `{
	"model": "claude-test-9",
	"summary": "routine changes",
	"risk_summary": {"concerns": [], "positives": []},
	"action_items": {"critical": [], "important": [], "followup": []},
	"technical_details": {"code": [], "infrastructure": [], "dependencies": []},
	"documentation_quality": "ok",
	"documentation_recommendations": "none"
}`

// renderFixture builds a minimal, registered fetch data directory doRender
// can consume. Callers must have pointed SOUNDINGS_CACHE_DIR at a test
// directory first.
func renderFixture(t *testing.T) string {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "soundings-fixture")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "index.json"),
		[]byte(`{"compare_urls":[],"repos":[],"guidance":[],"docs":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "guidance.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := registerDir(dataDir); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func TestDoRenderDeletesDataDirOnSuccess(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dataDir := renderFixture(t)
	if err := os.MkdirAll(filepath.Join(dataDir, "patches", "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(t.TempDir(), "soundings-report.md")

	result, validationErrs, err := doRender(validAnalysisJSON, dataDir, renderOpts{ReportPath: reportPath})
	if err != nil || len(validationErrs) > 0 {
		t.Fatalf("doRender: err=%v validationErrs=%v", err, validationErrs)
	}
	if result.Verdict != "release" {
		t.Errorf("verdict = %q, want %q", result.Verdict, "release")
	}
	if !strings.HasPrefix(result.ReportMarkdown, reportBanner) {
		t.Error("report_markdown does not start with the report banner")
	}
	if result.ReportPath != reportPath {
		t.Errorf("report_path = %q, want %q", result.ReportPath, reportPath)
	}
	if !strings.HasPrefix(result.SummaryMarkdown, reportBanner) || !strings.Contains(result.SummaryMarkdown, "**Recommendation:**") {
		t.Errorf("summary_markdown should be the report's opening section, got %q", result.SummaryMarkdown)
	}
	if strings.Contains(result.SummaryMarkdown, "## 🔍 Risk Analysis") {
		t.Error("summary_markdown must stop before the Risk Analysis section")
	}
	if !strings.HasPrefix(result.ReportMarkdown, result.SummaryMarkdown) {
		t.Error("summary_markdown must be a verbatim prefix of report_markdown")
	}

	// The run's products outlive it; the fetch data does not.
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Errorf("data dir should be deleted after a successful render (stat err=%v)", err)
	}
	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("report_path copy must outlive the data dir: %v", err)
	}
	if !strings.HasPrefix(string(content), reportBanner) {
		t.Errorf("report copy does not start with the report banner")
	}
	if dirs := allowedDirs(); len(dirs) != 0 {
		t.Errorf("registry should hold nothing after the run, got %v", dirs)
	}
}

func TestDoRenderPersistsAnalysisOnValidationFailure(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dataDir := renderFixture(t)

	// Fields outside the schema are rejected as unknown.
	_, validationErrs, err := doRender(`{"unrecognized_field": true}`, dataDir, renderOpts{ReportPath: filepath.Join(t.TempDir(), "report.md")})
	if err != nil {
		t.Fatalf("doRender: %v", err)
	}
	if len(validationErrs) == 0 {
		t.Fatal("expected validation errors")
	}
	// The directory, its registration, and the rejected analysis must all
	// survive for the retry loop.
	if _, err := os.Stat(filepath.Join(dataDir, "analysis.json")); err != nil {
		t.Errorf("analysis.json should be persisted even when validation fails: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "index.json")); err != nil {
		t.Errorf("data dir must survive a validation failure for the retry loop: %v", err)
	}
	if _, ok := lookupRegistered(dataDir); !ok {
		t.Error("the data dir must stay registered after a validation failure")
	}
}

func TestDoRenderRejectsUnregisteredDataDir(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	// A plausible-looking directory the helper's fetch never created.
	dataDir := filepath.Join(t.TempDir(), "archived-run")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "index.json"),
		[]byte(`{"compare_urls":[],"repos":[],"guidance":[],"docs":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "precious.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := doRender(validAnalysisJSON, dataDir, renderOpts{ReportPath: filepath.Join(t.TempDir(), "report.md")})
	if err == nil || !strings.Contains(err.Error(), "not a live fetch data directory") {
		t.Fatalf("expected a not-registered error, got %v", err)
	}
	// Nothing was written into or deleted from the unowned directory.
	if _, err := os.Stat(filepath.Join(dataDir, "precious.txt")); err != nil {
		t.Errorf("an unregistered directory must be left untouched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "analysis.json")); !os.IsNotExist(err) {
		t.Error("analysis.json must not be written into an unregistered directory")
	}
}

func TestDoRenderRejectsReportPathInsideDataDir(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dataDir := renderFixture(t)

	_, _, err := doRender(validAnalysisJSON, dataDir, renderOpts{ReportPath: filepath.Join(dataDir, "report-copy.md")})
	if err == nil || !strings.Contains(err.Error(), "inside a live fetch data directory") {
		t.Fatalf("expected a report_path-inside-data_dir error, got %v", err)
	}
	// The run is not consumed by the failed attempt: still on disk and
	// registered, so a corrected report_path can be retried.
	if _, ok := lookupRegistered(dataDir); !ok {
		t.Error("the data dir must stay registered after a rejected report_path")
	}
}

func TestDoRenderComputesVerdictFromConcerns(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())

	analysis := `{
		"model": "claude-test-9",
		"summary": "risky changes",
		"risk_summary": {"concerns": [{"severity": "high", "description": "auth change unverified"}], "positives": []},
		"action_items": {"critical": [], "important": [], "followup": []},
		"technical_details": {"code": [], "infrastructure": [], "dependencies": []},
		"documentation_quality": "ok",
		"documentation_recommendations": "none"
	}`

	reportPath := filepath.Join(t.TempDir(), "report.md")
	result, validationErrs, err := doRender(analysis, renderFixture(t), renderOpts{ReportPath: reportPath})
	if err != nil || len(validationErrs) > 0 {
		t.Fatalf("doRender: err=%v validationErrs=%v", err, validationErrs)
	}
	if result.Verdict != "review" {
		t.Errorf("verdict = %q, want %q (high concern under default block_on)", result.Verdict, "review")
	}
	if !strings.Contains(result.ReportMarkdown, "auth change unverified") {
		t.Error("report should cite the concern that drove the verdict")
	}

	// The same concern blocks outright under a stricter policy. (A fresh
	// fixture: a successful render deletes its data directory.)
	result, validationErrs, err = doRender(analysis, renderFixture(t), renderOpts{BlockOn: "high", ReportPath: reportPath})
	if err != nil || len(validationErrs) > 0 {
		t.Fatalf("doRender (block_on=high): err=%v validationErrs=%v", err, validationErrs)
	}
	if result.Verdict != "not_recommended" {
		t.Errorf("verdict = %q, want %q under block_on=high", result.Verdict, "not_recommended")
	}
}

func TestDoRenderRejectsInvalidBlockOn(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dataDir := renderFixture(t)

	if _, _, err := doRender(validAnalysisJSON, dataDir, renderOpts{BlockOn: "sometimes", ReportPath: filepath.Join(t.TempDir(), "report.md")}); err == nil {
		t.Error("expected an error for an invalid block_on value")
	}
}

func TestDoRenderRequiresReportPath(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dataDir := renderFixture(t)

	for _, tc := range []struct{ name, path string }{
		{"missing", ""},
		{"relative", "soundings-report.md"},
		{"not markdown", filepath.Join(t.TempDir(), "report.txt")},
	} {
		_, _, err := doRender(validAnalysisJSON, dataDir, renderOpts{ReportPath: tc.path})
		if err == nil || !strings.Contains(err.Error(), "report_path") {
			t.Errorf("%s: expected a report_path error, got %v", tc.name, err)
		}
	}
	// The usage mistake is caught before any work: the analysis is not
	// persisted and the data directory is neither touched nor released.
	if _, err := os.Stat(filepath.Join(dataDir, "analysis.json")); !os.IsNotExist(err) {
		t.Error("analysis.json must not be written when report_path is rejected")
	}
	if _, ok := lookupRegistered(dataDir); !ok {
		t.Error("the data dir must stay registered after a rejected report_path")
	}
}

func TestDoRenderOverwritesOwnReportCopy(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	reportPath := filepath.Join(t.TempDir(), "soundings-report.md")
	opts := renderOpts{ReportPath: reportPath}

	if _, validationErrs, err := doRender(validAnalysisJSON, renderFixture(t), opts); err != nil || len(validationErrs) > 0 {
		t.Fatalf("doRender: err=%v validationErrs=%v", err, validationErrs)
	}

	// A later run may overwrite a previously generated soundings report.
	// (A fresh fixture: a successful render deletes its data directory.)
	if _, validationErrs, err := doRender(validAnalysisJSON, renderFixture(t), opts); err != nil || len(validationErrs) > 0 {
		t.Fatalf("doRender (overwrite): err=%v validationErrs=%v", err, validationErrs)
	}
}

func TestWriteReportCopyGuards(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())

	if err := writeReportCopy("relative/report.md", "x"); err == nil {
		t.Error("expected an error for a relative report_path")
	}
	if err := writeReportCopy(filepath.Join(t.TempDir(), "report.txt"), "x"); err == nil {
		t.Error("expected an error for a non-.md report_path")
	}

	notAReport := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(notAReport, []byte("# my project"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeReportCopy(notAReport, "x"); err == nil {
		t.Error("expected a refusal to overwrite a file that is not a soundings report")
	}
	if content, _ := os.ReadFile(notAReport); string(content) != "# my project" {
		t.Error("existing non-report file must be left untouched")
	}
}
