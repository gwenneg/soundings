# Soundings

**Take soundings before you ship.**

Sailors have long measured the water's [depth](https://en.wikipedia.org/wiki/Depth_sounding)
ahead before committing a ship to a course. Soundings does the same for a
release: point it at one or more GitHub/GitLab compare URLs and it reads
the diffs, commit history, and reviewer discussion, then hands back one
clear verdict — release, manual review, or no-go, computed from the
severities of the risks it found and saying what drove it — and a
report specific enough to act on: named files, named functions,
concrete commands to run before you ship.

```
/soundings:analyze https://github.com/org/repo/compare/v1.0...v1.1
```

**No external LLM API, no service-account key, no Docker image.** The
Claude Code agent already in your session does the analysis; a small Go
helper (invoked via `go run`, nothing to install) handles the deterministic
work — fetching diffs and commit/PR metadata, extracting authorized
reviewer guidance, risk-tiering changed files, and rendering the report.
Nothing about a run leaves your machine except the requests you'd make
anyway to fetch the diff.

## Why Soundings

- **Multi-repo by design.** Pass several compare URLs from a coordinated,
  multi-service deployment in one invocation and it analyzes them
  together — catching the compound risk a per-repo CI check can't see
  (e.g. a client contract change on one side with no corresponding update
  on the other).
- **Reads with judgment, not a fixed diff limit.** Every changed file is
  risk-tiered (database/auth/API contracts are always read in full;
  low-risk files are skimmed) so large diffs get proportionate attention
  instead of being truncated blind or blowing the context budget.
- **Specific, not generic.** Reports cite the actual file, function, and
  line at issue and turn concerns into action items sorted by urgency —
  not a rubber-stamped "looks fine."
- **Gets better with your input.** A `.soundings-docs.md` file teaches it
  your service's criticality and known risk areas; a `/soundings note`
  comment on the PR/MR hands it context it can't infer from a diff alone.
  See the [analysis guide](docs/IMPROVING_ANALYSIS.md).
- **Built to be run unattended, safely.** It ingests diffs and PR/MR
  comments — text written by whoever opened the change — so every stage
  is scoped as if that text were hostile. See [Security model](#security-model).

## Example

<details>
<summary>Sample output (abridged)</summary>

```markdown
# 🚀 Release Readiness Report

## 🎯 Summary

A well-tested rate-limiter change, but the new retry logic in the payment
webhook handler has no test covering the exponential-backoff cap.

**Recommendation:** ⚠️ MANUAL REVIEW REQUIRED

Driven by 1 high concern — detailed in Risk Analysis below.

## 🔍 Risk Analysis

### Concerns
| | Details |
|---|---|
| ⚠️ | `internal/webhook/retry.go` raises the max retry count from 3 to 8 with no ceiling on total wall-clock time — a stalled downstream dependency could hold a webhook worker for over 4 minutes. No test exercises the new upper bound. |
| 🟡 | `config/rate_limits.yaml` doubles the per-tenant burst limit; no corresponding change to the load-test fixtures that assert on it. |

### Positive Factors
- Rate limiter change is covered by 6 new table-driven test cases.
- Dependency bumps are all patch-level, dependabot-authored.

## 📋 Action Items

### 🔥 Critical (Complete Before Release)
- Add a test asserting `retry.go`'s total backoff time is capped, and pick
  a cap value with the on-call team.
```

</details>

Full reports go further: a technical-details appendix listing what was
read in full versus skimmed, a changelog table per repository, and a
documentation-quality assessment — all in one Markdown file you can post
back to the PR/MR or keep locally.

## Install

Soundings is distributed through the
[claude-ichiba](https://github.com/gwenneg/claude-ichiba) marketplace —
no cloning or file editing. In Claude Code:

```
/plugin marketplace add gwenneg/claude-ichiba
/plugin install soundings@claude-ichiba
/reload-plugins
```

## Requirements

- [Claude Code](https://claude.com/claude-code)
- A Go toolchain (`brew install go`)
- `gh` and/or `glab` authenticated (or `GITHUB_TOKEN` / `GITLAB_TOKEN` set)

## Helper MCP server

The plugin bundles its Go helper as an MCP server exposing two tools,
`fetch` and `render`, started automatically per session. It is inert at
rest: no credentials are read and no network is touched until a tool is
called, and it can be toggled off in the `/mcp` panel. For containerized
headless use, build the binary into the image and point the MCP server
config at it instead of `go run`.

## Security model

Everything Soundings analyzes — diffs, commit messages, PR/MR comments,
repository documentation — is externally authored and treated as untrusted:

- **Fetching is code, not agent tool use.** A Go helper is the only
  component that touches the network. It resolves tokens per platform and
  per GitLab host (a token for one host is never sent to another), blocks
  private-IP destinations for repository-linked documents (dial-time SSRF
  checks covering redirects and DNS rebinding), and strips the GitLab token
  on any redirect that leaves the trusted host.
- **Reading is isolated.** The fetched content is read and assessed by a
  dedicated subagent (`risk-analyst`) restricted to read-only tools (Read,
  Grep, Glob) — no shell, no network, no writes — so a prompt injection
  inside a diff can at most skew the analysis text it returns, not drive
  tools in your session.
- **The verdict is computed, not written.** The renderer derives the
  release verdict from the schema-validated concern severities and the
  `block_on` policy — each severity attached to a human-checkable
  description, so flipping the verdict requires fabricating or
  suppressing a whole named concern, not nudging a number — validates
  the analysis against a schema, and escapes externally-authored text in
  the report: analysis prose cannot fake a verdict or forge report
  structure. Recognizable credentials (platform tokens, cloud keys, PEM
  blocks, JWTs) are redacted from the analysis before validation, so a
  secret that slips into the assessment never reaches the report.
- **Reads are confined, not just read-only.** The plugin bundles a
  PreToolUse hook (`go run . hook`) backed by a registry of the exact
  output directories the fetch step created (a per-user file under the OS
  cache directory). The hook approves the risk-analyst agent's Read, Grep,
  and Glob inside a registered directory — skipping the permission prompt,
  so the isolated stage runs unattended — and denies them anywhere else, so
  injected content cannot steer it into reading or searching secrets
  elsewhere on disk (`~/.ssh`, `.env` files) and leaking them into the
  analysis. The same hook confines in the other direction too: every other
  agent, the orchestrating session included, is denied those tools
  wherever they would reach inside a registered directory — reads inside
  it, recursive searches rooted above it, absolute glob patterns anchored
  near it — so the read tools cannot pull untrusted fetched content into
  any other agent's context (the analyze skill's turn separately disallows
  shell and network tools). The directory itself is helper-owned from
  creation to deletion, and only a directory the registry vouches for is
  ever rendered from — or deleted: a successful render deletes the fetched
  data and withdraws its registration together, a failed fetch cleans up
  behind itself, and for an abandoned run the risk-analyst's authorization
  expires after 24 hours while the keep-out holds for as long as the data
  exists — the next fetch deletes both together. Every
  registry failure reads as "not registered" and denies the risk-analyst —
  it fails closed. User-configured deny rules always override the hook's
  approval.

  Whether plugin-bundled hooks fire inside subagents is not yet documented
  behavior; to make the confinement unconditional, register the same hook
  in your own settings:

  ```json
  {
    "hooks": {
      "PreToolUse": [
        {
          "matcher": "Read|Grep|Glob",
          "hooks": [
            { "type": "command",
              "command": "go -C <path-to-soundings> run . hook" }
          ]
        }
      ]
    }
  }
  ```

  As a coarse second net independent of Soundings, harness-enforced deny
  rules in `settings.json` keep high-value secret paths unreadable for the
  whole session, subagents included — deny rules always beat allow rules:

  ```json
  {
    "permissions": {
      "deny": [
        "Read(~/.ssh/**)",
        "Read(~/.aws/**)",
        "Read(**/.env)",
        "Read(**/.env.*)"
      ]
    }
  }
  ```

The residual risk — an injection biasing a concern's severity or the
analysis wording — is inherent to any LLM-based review and is why every
report is marked advisory; the severity-driven verdict at least forces
that bias into the most auditable place, a named concern a human will
read.

## Running without permission prompts

The bundled PreToolUse hook pre-approves both prompt sources a skill run
would otherwise have: the risk-analyst stage's reads inside the registered
fetch directory, and the plugin's own helper MCP tools (`fetch` and
`render`). The skill's `allowed-tools` frontmatter constrains what the turn
may use but is not a permission grant, and a plugin cannot ship permission
rules — the hook's explicit allow is the plugin-side mechanism that skips
the prompt. User-configured deny and ask rules always override it. If you
run with the plugin's hooks disabled, the equivalent settings rules are
`mcp__plugin_soundings_helper__fetch` and
`mcp__plugin_soundings_helper__render` in `permissions.allow`.

File writes never prompt either, because no agent performs them: the
helper itself persists the analysis JSON for the validation retry loop
(a successful render deletes the data directory, so the report lives on
as `report_markdown` in the tool result), and writes the caller-chosen `report_path` copy (absolute
`.md` path only; an existing file is only overwritten when it is a
previously generated Soundings report, so the auto-approved tool cannot be
steered into clobbering arbitrary files). The analyze skill turn disallows
the Write tool outright — every file this pipeline produces is written by
the helper. A normal run is fully prompt-free.

## Status

Under active development. Every generated report links to a short
[feedback form](https://forms.gle/qkinM8bZ4uCDDWsL8) — it directly shapes
what changes next.

## Provenance

Soundings is the skills-based successor to
[Release Confidence Score](https://github.com/RedHatInsights/release-confidence-score)
(RCS), originally built at Red Hat and licensed under Apache-2.0. The core Go
packages (git providers, SSRF-hardened HTTP client, risk patterns, report
renderer) were extracted from RCS with full git history preserved — original
authorship is visible in this repository's log. See [NOTICE](NOTICE).

## License

[Apache-2.0](LICENSE)
