# Improving Your Release Readiness Analysis

This guide explains how to get more accurate verdicts and better analysis from Soundings.

## Quick Wins

### 1. Add Repository Documentation

**Impact: High** | **Effort: Medium** | **One-time setup**

Create a `.soundings-docs.md` file in your repository root. This gives the AI context about your service, its criticality, dependencies, and known risk areas.

```markdown
# Release Documentation

## Service Overview
**Service Criticality:** High
**Description:** User authentication service handling 10M requests/day
**SLA:** 99.9% uptime, <200ms P95 response time

## Deployment Risks & Considerations
### High-Risk Changes
1. **Database migrations** - Require 15-minute maintenance window
2. **OAuth configuration** - Affects all user logins

## Rollback Procedures
1. Revert to previous container tag
2. Run database down migration if needed

## Additional Documentation
- [Runbook](./docs/runbook.md)
- [Architecture](./docs/architecture.md)
```

**Why this helps:**
- AI understands your service's criticality level
- Analysis considers your specific risk areas
- Recommendations are tailored to your architecture

See [`.soundings-docs.example.md`](../.soundings-docs.example.md) for a comprehensive template.

---

### 2. Use User Guidance Comments

**Impact: High** | **Effort: Low** | **Per-PR/MR**

Add `/soundings note` comments in your pull request or merge request to provide context the AI can't infer from code alone:

```
/soundings note This change updates the rate limiting logic. The new limits have been
load tested and approved by the platform team. No database changes required.
```

**Effective guidance includes:**
- Why changes are safe despite appearing risky
- Context about testing that was performed
- Dependencies or sequencing requirements
- Business context the AI can't see

**Authorization:** Only guidance from PR/MR authors and approvers is used in the analysis. Other comments are shown in the report but not factored into the assessment.

---

### 3. Write Clear Commit Messages

**Impact: Medium** | **Effort: Low** | **Per-commit**

Use descriptive commit messages that explain intent, not just action:

**Less helpful:**
```
fix bug
update config
```

**More helpful:**
```
fix: prevent session leak when user logs out during request

The previous implementation didn't cancel in-flight requests,
leaving orphaned sessions in Redis.
```

**Why this helps:**
- AI understands the purpose and impact of changes
- Risk assessment is more accurate
- Related changes are easier to identify

---

## Optimizing Your Workflow

### 4. Keep Changes Focused

**Impact: Medium** | **Effort: Medium**

Separate high-risk and low-risk changes:

**High-risk (separate PRs/MRs):**
- Database schema changes
- Authentication/authorization
- API contract modifications
- Infrastructure configuration
- Security-related code

**Low-risk (can combine):**
- Documentation updates
- Test additions
- Code formatting
- Comment improvements

**Why this helps:**
- Easier to analyze and understand each change's impact
- More accurate verdicts
- Simpler rollback if issues arise

---

### 5. Manage Diff Size

**Impact: Low-Medium** | **Effort: Medium**

Large diffs are handled by reading with judgment rather than truncating: the
analysis always sees every file's metadata, statistics, and risk tier, reads
critical and high-risk patches in full, and decides how deeply to read the
rest. Smaller, focused changes still get the most thorough treatment —
everything is read in full, and the report says what was read versus skimmed.

**Tips for large changes:**
- Break features into incremental PRs/MRs
- Separate refactoring from functional changes
- Use feature flags for gradual rollout

---

### 6. Link Related Documentation

**Impact: Low-Medium** | **Effort: Low**

In your `.soundings-docs.md`, add links in the "Additional Documentation" section:

```markdown
## Additional Documentation
- [Runbook](./docs/runbook.md)
- [API Reference](./docs/api.md)
- [Architecture Overview](https://wiki.example.com/my-service)
```

**Suggested priority when choosing what to link:**
1. Runbooks - Deployment and operational procedures
2. Monitoring guides - Health checks and alerting
3. Architecture docs - System design and dependencies
4. API documentation - Contract and integration details

The tool fetches all links equally - this is just guidance on what tends to be most useful for risk analysis. Only links in this section are fetched; links elsewhere are for human reference only.

---

## Understanding Your Verdict

### How the Verdict Is Computed

The verdict is derived deterministically from the severities of the
concerns the analysis finds, under the `block_on` policy (default
`critical`):

| Finding | Verdict |
|---------|---------|
| Any concern at or above `block_on` | 🚫 Release not recommended |
| Any concern one severity below `block_on`, or an outstanding "complete before release" action item | ⚠️ Manual review required |
| Neither | ✅ Recommended for release |

Every verdict says what drove it — the severity and count of the driving
concerns, detailed in the Risk Analysis section — so improving your
verdict means resolving (or providing evidence that de-escalates) those
specific concerns.

### Factors That Reduce Concern Severity

- Comprehensive repository documentation
- Test coverage evidenced in the diff (tests changed alongside code)
- Small, focused changes
- Clear commit messages
- User guidance providing context
- Changes to low-risk files (tests, docs)
- Documented rollback procedures

### Factors That Raise Concern Severity

- Missing or sparse documentation
- High-risk changes (database, auth, API contracts)
- Large, complex diffs
- Multiple unrelated changes in one PR/MR
- Critical paths changed without accompanying tests
- Unclear purpose or impact
- Infrastructure changes without context

### Risk Categories

The tool classifies files by risk tier to prioritize how deeply each is read:

**Critical (always read in full):**
- Database: migrations, schema files, SQL
- Security: auth, tokens, credentials, permissions
- APIs: OpenAPI specs, protobuf, GraphQL schemas

**High (read in full unless very large):**
- Infrastructure: Dockerfile, Terraform, Kubernetes
- Configuration: CI/CD pipelines, build configs
- Deployment: Helm charts, Ansible playbooks

**Medium (read proportionately to size):**
- Application code
- Dependencies: package.json, go.mod, requirements.txt
- Lock files: package-lock.json, go.sum

**Low (skimmed, used as evidence of coverage):**
- Tests: *_test.go, *.spec.js, test_*.py
- Documentation: *.md, docs/
- IDE/tooling: .vscode/, .editorconfig

---

## Common Scenarios

### "My verdict is stricter than expected"

**Checklist:**
- [ ] Did you add `/soundings note` guidance explaining the changes?
- [ ] Is repository documentation present and current?
- [ ] Are commit messages descriptive?
- [ ] Is the PR/MR focused on one concern?

**Quick fix:** Add a `/soundings note` comment explaining why changes are safe.

---

### "The AI doesn't understand my service"

**Solution:** Create or improve `.soundings-docs.md`

Include:
- Service criticality and SLA
- Known risk areas specific to your service
- Dependencies and integration points
- Rollback procedures
- Historical issues to watch for

---

### "Low-risk changes are flagged as risky"

**Common causes:**
1. File names match high-risk patterns (e.g., `config` in the path)
2. Missing context about what the files do
3. Mixed with higher-risk changes

**Solutions:**
- Add `/soundings note` guidance: `/soundings note These config files are test fixtures, not production configuration`
- Separate low-risk changes into their own PR/MR
- Update repository documentation to clarify

---

### "Large refactoring PR/MR"

**Best practices:**
1. Add detailed `/soundings note` guidance explaining the scope
2. Confirm behavior is unchanged: `/soundings note Pure refactoring, no behavior changes. All tests pass.`
3. Reference test results or validation evidence
4. Consider breaking into smaller incremental changes

---

## Best Practices Summary

### Before Submitting

- [ ] Repository has `.soundings-docs.md`
- [ ] Added `/soundings note` guidance with relevant context
- [ ] Commit messages explain "why" not just "what"
- [ ] PR/MR is focused on a single concern
- [ ] Documentation updated to reflect changes

### After Receiving Report

- [ ] Review all action items, especially "Critical"
- [ ] Understand identified risk factors
- [ ] Address documentation recommendations
- [ ] Add clarifying `/soundings note` guidance if AI missed context
- [ ] Re-run analysis if significant context was added

---

## Configuration Reference

### Blocking Policy

`block_on` is a parameter of the analysis invocation: the severity at or
above which a concern blocks the release (`critical` by default; `high`
or `medium` for stricter gating of critical services). Concerns one
severity below it produce a manual-review verdict. Callers such as
orchestrating skills may pass their own value.


## Getting Help

### The Analysis Seems Wrong

1. **Add context via `/soundings note`** - The AI works with available information
2. **Update documentation** - Ensure `.soundings-docs.md` reflects reality
3. **Check read depth** - The report notes what was read in full versus skimmed; very large diffs mean lighter reading of low-risk files
4. **Review action items** - Sometimes the AI catches real issues

### Contributing

Report issues or suggestions at the [project repository](https://github.com/gwenneg/soundings).

---

**Remember:** The tool provides guidance to support decision-making, not replace it. Use it to surface risks and structure your analysis, combined with your team's judgment and domain expertise.
