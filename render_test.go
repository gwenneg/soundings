package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validAnalysisJSON = `{
	"model": "claude-test-9",
	"score": 85,
	"summary": "routine changes",
	"risk_summary": {"concerns": [], "positives": []},
	"action_items": {"critical": [], "important": [], "followup": []},
	"technical_details": {"code": [], "infrastructure": [], "dependencies": []},
	"documentation_quality": "ok",
	"documentation_recommendations": "none"
}`

// renderFixture builds a minimal fetch data directory doRender can consume.
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
	return dataDir
}

func defaultRenderOpts() renderOpts {
	return renderOpts{AutoDeploy: 80, ReviewRequired: 60}
}

func TestDoRenderPersistsAnalysisAndReport(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dataDir := renderFixture(t)

	result, validationErrs, err := doRender(validAnalysisJSON, dataDir, defaultRenderOpts())
	if err != nil || len(validationErrs) > 0 {
		t.Fatalf("doRender: err=%v validationErrs=%v", err, validationErrs)
	}
	if result.Score != 85 {
		t.Errorf("score = %d, want 85", result.Score)
	}

	saved, err := os.ReadFile(filepath.Join(dataDir, "analysis.json"))
	if err != nil {
		t.Fatalf("analysis.json not persisted: %v", err)
	}
	if !strings.Contains(string(saved), `"score": 85`) {
		t.Errorf("analysis.json does not contain the analysis: %q", saved)
	}

	report, err := os.ReadFile(filepath.Join(dataDir, "report.md"))
	if err != nil {
		t.Fatalf("report.md not persisted: %v", err)
	}
	if !strings.HasPrefix(string(report), reportBanner) {
		t.Errorf("report.md does not start with the report banner")
	}
}

func TestDoRenderPersistsAnalysisOnValidationFailure(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dataDir := renderFixture(t)

	_, validationErrs, err := doRender(`{"score": 300}`, dataDir, defaultRenderOpts())
	if err != nil {
		t.Fatalf("doRender: %v", err)
	}
	if len(validationErrs) == 0 {
		t.Fatal("expected validation errors")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "analysis.json")); err != nil {
		t.Errorf("analysis.json should be persisted even when validation fails: %v", err)
	}
}

func TestDoRenderWritesReportCopy(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dataDir := renderFixture(t)
	reportPath := filepath.Join(t.TempDir(), "soundings-report.md")

	opts := defaultRenderOpts()
	opts.ReportPath = reportPath
	if _, validationErrs, err := doRender(validAnalysisJSON, dataDir, opts); err != nil || len(validationErrs) > 0 {
		t.Fatalf("doRender: err=%v validationErrs=%v", err, validationErrs)
	}

	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("report copy not written: %v", err)
	}
	if !strings.HasPrefix(string(content), reportBanner) {
		t.Errorf("report copy does not start with the report banner")
	}

	// A second run may overwrite its own report.
	if _, validationErrs, err := doRender(validAnalysisJSON, dataDir, opts); err != nil || len(validationErrs) > 0 {
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
