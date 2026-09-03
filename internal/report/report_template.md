**⚠️ AI-Generated Report** — This report is AI-generated and advisory. Always review AI-generated content prior to use.

# 🚀 Release Readiness Report

## 🎯 Summary

{{escapeCell .Analysis.Summary}}

**Recommendation:** {{.ReleaseRecommendation}}

{{- if .VerdictReasons}}

{{verdictDrivers .VerdictReasons}}
{{- else}}

No blocking concerns found.
{{- end}}

---

{{- if .TruncationInfo}}

<details>
<summary><strong>✂️ Diff Truncation Applied</strong></summary>

Due to the large size of some changed files, their patches were truncated
before analysis:

- **Files fully analyzed:** {{.TruncationInfo.FilesPreserved}}/{{.TruncationInfo.TotalFiles}}
- **Files truncated:** {{.TruncationInfo.FilesTruncated}}
{{- range .TruncationInfo.TruncatedFiles}}
  - `{{escapeCell .}}`
{{- end}}

Critical-risk files (database, security, auth, API contracts) are never
truncated, however large. Truncated files keep their beginning and end;
only the middle section is omitted.

</details>

---
{{- end}}

## 🔍 Risk Analysis

{{- if .Analysis.RiskSummary.Concerns}}

### Concerns

***Severity:** 🔥 Critical · ⚠️ High · 🟡 Medium · 🟢 Low*

| | Details |
|----------|---------|
{{- range .Analysis.RiskSummary.Concerns}}
| {{severityEmoji .Severity}} | {{escapeCell .Description}} |
{{- end}}
{{- end}}

{{- if .Analysis.RiskSummary.Positives}}

### Positive Factors
{{- range .Analysis.RiskSummary.Positives}}
- {{escapeCell .}}
{{- end}}
{{- end}}

---

## 📋 Action Items

{{- if .Analysis.ActionItems.Critical}}

### 🔥 Critical (Complete Before Release)
{{- range .Analysis.ActionItems.Critical}}
- {{escapeCell .}}
{{- end}}
{{- end}}

{{- if .Analysis.ActionItems.Important}}

### ⚠️ Important (Recommended Before Release)
{{- range .Analysis.ActionItems.Important}}
- {{escapeCell .}}
{{- end}}
{{- end}}

{{- if .Analysis.ActionItems.Followup}}

### 📝 Follow-up (Post-Release)
{{- range .Analysis.ActionItems.Followup}}
- {{escapeCell .}}
{{- end}}
{{- end}}

{{- if .AllUserGuidance}}

---

## 📝 User Guidance

The following user guidance was provided in GitLab MR and GitHub PR discussions:

| Guidance | Author | Date | Status | Comment |
|----------|--------|------|--------|---------|
{{- range .AllUserGuidance}}
| {{escapeCell .Content}} | {{formatAuthor .Author .CommentURL}} | {{formatDate .Date}} | {{guidanceStatus .}} | [View]({{.CommentURL}}) |
{{- end}}

**Note:** Only authorized `/soundings note` guidance is used in the analysis. For GitHub PRs, that means the PR author or an approving reviewer with repository authority. For GitLab MRs, the MR author or anyone in the approver list. Unauthorized guidance is listed here for transparency but is ignored during the analysis. External guidance is supplied directly by the caller rather than sourced from a fetched PR/MR - it is neither verified nor used in the analysis, and is listed here for transparency only.
{{- end}}

---

<details>
<summary><strong>🛠️ Technical Details</strong></summary>

{{- if .Analysis.TechnicalDetails.Code}}

### 📝 Code Changes
{{- range .Analysis.TechnicalDetails.Code}}
- {{escapeCell .}}
{{- end}}
{{- end}}

{{- if .Analysis.TechnicalDetails.Infrastructure}}

### 🏗️ Infrastructure Changes
{{- range .Analysis.TechnicalDetails.Infrastructure}}
- {{escapeCell .}}
{{- end}}
{{- end}}

{{- if .Analysis.TechnicalDetails.Dependencies}}

### 🔗 Dependency Changes
{{- range .Analysis.TechnicalDetails.Dependencies}}
- {{escapeCell .}}
{{- end}}
{{- end}}

</details>

---

<details>
<summary><strong>📋 Changelogs</strong></summary>

{{- if .Comparisons}}
{{- range $i, $comparison := .Comparisons}}
{{- if gt $i 0}}

{{- end}}

### [{{$comparison.RepoURL}}]({{$comparison.DiffURL}})

{{- if $comparison.Commits}}
*Total commits: {{len $comparison.Commits}}*

| SHA | Message | Author | PR |
|-----|---------|--------|----|
{{- range $comparison.Commits}}
| {{commitLink .ShortSHA .SHA $comparison.RepoURL $comparison.Platform}} | {{escapePipes .Message}} | {{escapePipes .Author}} | {{prLink .PRNumber $comparison.RepoURL $comparison.Platform}} |
{{- end}}
{{- else}}
*No commits found in this comparison.*
{{- end}}
{{- end}}
{{- else}}
No repository changelog data available.
{{- end}}

</details>

---

<details>
<summary><strong>📚 Release Documentation</strong></summary>

### 📁 Documentation Sources Analyzed
{{- if .Documentation}}
{{- range .Documentation}}
{{- $doc := .}}
{{escapeCell (docFileInfo .MainDocFile .Repository.URL .Repository.DefaultBranch .Repository.Platform .MainDocContent)}}
{{- range $filename := .AdditionalDocsOrder}}
{{- if index $doc.AdditionalDocsContent $filename}}
{{escapeCell (docFileInfo $filename $doc.Repository.URL $doc.Repository.DefaultBranch $doc.Repository.Platform (index $doc.AdditionalDocsContent $filename))}}
{{- end}}
{{- end}}
{{- if .FailedAdditionalDocs}}

**Failed to fetch the following additional documentation:**
{{- range $displayName, $errorMsg := .FailedAdditionalDocs}}
- **{{escapeCell $displayName}}**: {{escapeCell $errorMsg}}
{{- end}}
{{- end}}
{{- end}}
{{- else}}
No repository documentation was found or analyzed.
{{- end}}

### 🔍 Overall Assessment
{{escapeCell .Analysis.DocumentationQuality}}

### 💡 Improvement Recommendations
{{escapeCell .Analysis.DocumentationRecommendations}}

</details>

---

<details>
<summary><strong>📈 Want Better Analysis Results?</strong></summary>

Learn how to get more accurate verdicts and sharper analysis:

👉 **[Guide: Improving Your Release Readiness Analysis](https://github.com/gwenneg/soundings/blob/main/docs/IMPROVING_ANALYSIS.md)**

**Quick tips:**
- Add `.soundings.md` to your repository for context-aware analysis
- Use `/soundings note` comments to provide context the AI can't infer from code
- Keep PRs/MRs focused and reasonably sized for better analysis quality

</details>

---

*🤖 Generated by [Soundings](https://github.com/gwenneg/soundings) | {{escapeCell .Metadata.ModelID}} | {{.Metadata.GenerationTime.Format "2006-01-02 15:04:05 UTC"}}*
