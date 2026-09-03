# Soundings

**Take soundings before you ship.**

Soundings is a [Claude Code](https://claude.com/claude-code) plugin that
reads a release before it ships, the way sailors
[sound the depth](https://en.wikipedia.org/wiki/Depth_sounding) ahead
before committing to a course. Point it at one or more GitHub/GitLab
compare URLs:

```
/soundings:analyze https://github.com/org/repo/compare/v1.0...v1.1
```

It reads the diffs, the commit messages, the PR/MR discussion, and the
repository documentation, then hands back one clear verdict (release,
manual review, or no-go) and a report specific enough to act on: named
files, named functions, concrete failure modes, and action items sorted by
urgency. Nothing to deploy, no LLM API key: the analysis runs in the Claude
Code session you already have, with the Git credentials you already use.

> Read the introduction post:
> [Take soundings before you ship](https://gwenneg.com/2026/09/03/take-soundings-before-you-ship.html).

## Why Soundings

- **Finds the risks a release manager looks for:** database migrations,
  auth and secrets, API contracts, infrastructure and config, resilience
  settings (retries, timeouts, rate limits, pools).
- **Multi-repo by design:** pass every compare URL of a coordinated
  deployment and they are analyzed together, catching compound risks that
  per-repo CI can never see.
- **Specific, not generic:** every concern names the file and the failure
  mode; every action item is a command to run or a check to make.
- **The verdict is computed, not written:** a fixed policy derives it from
  concern severities, so a wrong verdict traces back to one named concern
  you can go verify.
- **Safe on untrusted input:** fetching is code, reading happens in an
  isolated read-only subagent fenced off from your machine, and a prompt
  injection in a diff can at worst change what the analysis says.

## Quick start

You need [Claude Code](https://claude.com/claude-code), a Go toolchain
(`brew install go`), `git`, and a credential for each platform you analyze:
a `gh` or `glab` login, or `GITHUB_TOKEN` / `GITLAB_TOKEN`. No LLM API
key, no service account, no Docker image, no config file.

Install from the [claude-ichiba](https://github.com/gwenneg/claude-ichiba)
marketplace:

```
/plugin marketplace add gwenneg/claude-ichiba
/plugin install soundings@claude-ichiba
/reload-plugins
```

Then run it. The skill asks where to save the report unless you say so:

```
/soundings:analyze https://github.com/org/repo/compare/v1.0...v1.1 save the report to ./reviews/v1.1.md
```

A few minutes later the verdict shows up in your session: the summary, the
recommendation, and what drove it. The full report is the Markdown file,
ready to archive or to post where the ship decision happens. A normal run
needs no permission prompts.

Claude Code only auto-updates plugins from its official marketplace.
Enable auto-update for claude-ichiba in the `/plugin` panel, or refresh it
yourself with `/plugin marketplace update claude-ichiba` and
`/reload-plugins`.

## Usage

| Platform | Compare URL |
|----------|-------------|
| GitHub (github.com) | `https://github.com/<owner>/<repo>/compare/<base>...<head>` |
| GitLab (gitlab.com or self-managed) | `https://<host>/<group>/<project>/-/compare/<base>...<head>` |

`<base>` and `<head>` can be tags, branches, or SHAs. One invocation can
mix github.com and any number of GitLab hosts; Soundings picks the right
credential for each, and a token for one host is never sent to another.

```
/soundings:analyze https://github.com/org/api/compare/v2.3.0...v2.4.0 https://gitlab.example.com/org/gateway/-/compare/v1.8.0...v1.9.0
```

Options are given in plain language in the invocation:

| Option | Meaning |
|--------|---------|
| Report path | Must end in `.md`, in a directory that exists. Only a previous Soundings report is ever overwritten. Required up front in headless runs. |
| Blocking policy | Severity at or above which a concern blocks the release: `critical` (default), `high`, or `medium`. "Block on high" tightens it for critical services. |
| Notes | Risk-relevant context you vouch for, handed to the analysis. |

## What you get

<details>
<summary>Top of a report (from the demo analysis of a two-service release)</summary>

```markdown
**⚠️ AI-Generated Report** — This report is AI-generated and advisory. Always review AI-generated content prior to use.

# 🚀 Release Readiness Report

## 🎯 Summary

Multi-service release of soundings-demo-api and soundings-demo-gateway with a database migration, email connector changes, and significant API changes requiring careful deployment coordination

**Recommendation:** 🚫 **RELEASE NOT RECOMMENDED**

Driven by 1 critical concern — detailed in Risk Analysis below.

---

## 🔍 Risk Analysis

### Concerns

| | Details |
|---|---|
| 🔥 | Database migration `V35__add_severity_column_on_event_table.sql` adds `severity` column + matching JPA field in `src/main/java/com/gwenneg/soundingsdemo/models/Event.java` - violates critical deployment rule requiring split releases |
| ⚠️ | COMPOUND: retry + timeout. HTTP retry count for email delivery service increased from 2 to 5 in `src/main/java/com/gwenneg/soundingsdemo/connectors/EmailConnector.java` + HTTP timeout per attempt increased from 200ms to 1s in `src/main/resources/application.properties` - each change is individually within the 2s public API SLO but combined worst case may exceed it when the email service is degraded |

### Positive Factors
- New bulk export endpoint gated behind feature flag `FEATURE_BULK_EXPORT` in `src/main/java/com/gwenneg/soundingsdemo/config/FeatureConfig.java` - can be disabled without redeployment
- `soundings-demo-gateway` event routing alignment in PR #342 was verified compatible with both old and new `soundings-demo-api` versions

---

## 📋 Action Items

### 🔥 Critical (Complete Before Release)
- BLOCK DEPLOYMENT: Split release into two parts - deploy SQL migration `V35` first, then deploy code changes with `severity` field in separate release
```

</details>

The critical concern above caught a migration and the code using its new
column shipping together, against the service's own deployment rules. The
compound one connected a retry count raised in one file with a timeout
raised in another, which no single-file review would do. The last positive
factor is only visible because both repositories were analyzed together.

Your session shows the opening section. The file adds action items in
three urgency levels, every `/soundings note` found with its authorization
status, technical details with what was read in full versus skimmed, a
changelog per repository, and an assessment of the repository
documentation. Read the [full demo report](docs/DEMO_REPORT.md).

## How the verdict is computed

| Finding | Verdict |
|---------|---------|
| Any concern at or above the blocking severity | 🚫 Release not recommended |
| Any concern one severity below it, or an outstanding "complete before release" action item | ⚠️ Manual review required |
| Neither | ✅ Recommended for release |

With the default policy, any critical concern blocks the release and any
high concern requires manual review. Severities are assigned
conservatively: incomplete evidence means the higher severity, and a
mitigation only lowers one when it can be cited by file or name.

## Getting better results

Two inputs make the analysis noticeably sharper.

- **A `.soundings.md` file in your repository root** tells the analyst what
  the service is, how critical it is, and where the known risky areas are:
  SLOs, deployment rules such as "migrations ship in their own release",
  rollback procedures, links to runbooks. That is where the demo's blocking
  concern came from. Start from [`.soundings.example.md`](.soundings.example.md).
- **A `/soundings note` comment on a PR or MR** hands the analysis context a
  diff cannot show, such as a load test that was run. Notes only count when
  written by the PR/MR author or an authorized approver, as verified
  through the platform API; other notes are disclosed in the report but
  ignored.

The full guide is [Improving your release readiness analysis](docs/IMPROVING_ANALYSIS.md).

## Security

Everything Soundings reads could have been written by anyone, so the
design assumes every fetched byte is hostile and the protections are
limits the AI cannot cross. A run adds no channel of its own (no extra LLM
API, no telemetry); the fetching is done by an SSRF-hardened Go helper,
and each agent gets only the permissions its job needs:

| | Main agent (your session) | `risk-analyst` subagent |
|---|---|---|
| Shell, file edits, network | Switched off during the run | Never had them |
| Your project files | Can read them | Cannot read them |
| The fetched release data | Cannot read it | The only thing it can read |

Every file is written by the helper, credentials are redacted from the
analysis, the verdict is computed from severities, and the fetched data is
deleted when the run ends. The residual risk, an injection biasing the
wording or a severity, is inherent to any LLM-based review and is why
every report is marked advisory.

[Security model](docs/SECURITY.md) has the full picture, the optional
hardening, and how to run unattended or in a container.

## Troubleshooting

See [Troubleshooting](docs/TROUBLESHOOTING.md) for authentication errors,
unsupported URLs, denied reads, and helper logs.

## Development

Run `go test ./...`. Commit messages follow
[Conventional Commits](https://www.conventionalcommits.org/): the release
workflow derives the next version from them, and merging the standing
release PR publishes to the marketplace.

## Status and roadmap

Under active development; [issues and ideas](https://github.com/gwenneg/soundings/issues)
directly shape what changes next. Next up: headless runs as a gate in a
delivery pipeline (a clean verdict promotes, anything else hands the
decision back to a human with the report explaining why), and a port to
Codex. Red Hat associates shipping through app-interface can pair
Soundings with the
[soundings-app-interface](https://github.com/gwenneg/soundings-app-interface)
adapter.

## Provenance and license

Soundings is the successor to
[Release Confidence Score](https://github.com/RedHatInsights/release-confidence-score),
originally built at Red Hat under Apache-2.0. Its core was extracted with
full git history preserved; see [NOTICE](NOTICE). Soundings is licensed
under [Apache-2.0](LICENSE).
