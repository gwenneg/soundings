# Soundings

**Take soundings before you ship.**

Soundings is a Claude Code plugin that analyzes code changes — one or more
GitHub/GitLab compare URLs — and produces a release confidence score (0–100)
with a comprehensive, actionable risk report: categorized concerns, compound-risk
detection across repositories, and actionable action items.

```
/soundings:analyze https://github.com/org/repo/compare/v1.0...v1.1
```

The agent running the skill does the analysis itself — there is no external
LLM API to configure, no service-account key, and no Docker image. A small Go
helper (invoked via `go run`, no installation) handles the deterministic work:
fetching diffs and commit/PR metadata, extracting authorized reviewer guidance,
risk-tiering changed files, and rendering the final report.

## Helper MCP server

The plugin bundles its Go helper as an MCP server exposing two tools,
`fetch` and `render`, started automatically per session. It is inert at
rest: no credentials are read and no network is touched until a tool is
called, and it can be toggled off in the `/mcp` panel. For containerized
headless use, build the binary into the image and point the MCP server
config at it instead of `go run`.

## Requirements

- [Claude Code](https://claude.com/claude-code)
- A Go toolchain (`brew install go`)
- `gh` and/or `glab` authenticated (or `GITHUB_TOKEN` / `GITLAB_TOKEN` set)

## Security model

Everything soundings analyzes — diffs, commit messages, PR/MR comments,
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
  recommendation banner from the numeric score and thresholds, validates
  the analysis against a schema, and escapes externally-authored text in
  the report — analysis prose cannot fake a verdict or forge report
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
  analysis. A successful render withdraws the run's authorization; crashed
  runs expire from the registry after 24 hours. Every registry failure
  reads as "not registered" and denies — it fails closed. User-configured
  deny rules always override the hook's approval.
  Whether plugin-bundled hooks fire inside subagents is not yet
  documented behavior; to make the confinement unconditional, register
  the same hook in your own settings:

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

As a coarse second net independent of soundings, harness-enforced deny
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

The residual risk — an injection biasing the score or analysis wording —
is inherent to any LLM-based review and is why every report is marked
advisory.

## Running without permission prompts

The bundled PreToolUse hook pre-approves both prompt sources a skill run
would otherwise have: the risk-analyst stage's reads inside the registered
fetch directory, and the plugin's own helper MCP tools (`fetch` and
`render`). The skill's `allowed-tools` frontmatter constrains what the turn
may use but is not a permission grant, and a plugin cannot ship permission
rules - the hook's explicit allow is the plugin-side mechanism that skips
the prompt. User-configured deny and ask rules always override it. If you
run with the plugin's hooks disabled, the equivalent settings rules are
`mcp__plugin_soundings_helper__fetch` and
`mcp__plugin_soundings_helper__render` in `permissions.allow`.

The one remaining prompt is the orchestrator writing the analysis JSON to
disk (a file-modification prompt, kept for the validation retry loop).

## Status

Under active development.

## Provenance

Soundings is the skills-based successor to
[Release Confidence Score](https://github.com/RedHatInsights/release-confidence-score)
(RCS), originally built at Red Hat and licensed under Apache-2.0. The core Go
packages (git providers, SSRF-hardened HTTP client, risk patterns, report
renderer) were extracted from RCS with full git history preserved — original
authorship is visible in this repository's log. See [NOTICE](NOTICE).

## License

[Apache-2.0](LICENSE)
