# Security model

Everything Soundings analyzes (diffs, commit messages, PR/MR comments,
repository docs) could have been written by anyone. Even your own release
carries text you did not write: other people's code, bot commits, comments
from anyone with an account. So the design assumes every byte it fetches
is hostile, and the protections are limits the AI cannot cross, not
instructions it is asked to follow. The
[introduction post](https://gwenneg.com/2026/09/03/take-soundings-before-you-ship.html#why-its-safe-to-run-on-your-laptop)
tells the same story in narrative form.

**What a run talks to.** A run adds no channel of its own: no extra LLM
API, no telemetry, no server collecting anything. It only reaches the
platforms that host your code, and the analysis stays in your existing
Claude Code session, like the rest of your work. Even that traffic never
comes from the AI: the fetching is done by the Go helper with an
SSRF-hardened HTTP client that blocks private and internal addresses at
dial time (covering redirects and DNS rebinding), so a document link
planted in a repository cannot make your laptop probe your internal network
or a cloud metadata endpoint. A GitLab token is only ever sent to its own
host, and is stripped from any redirect that leaves it.

**Who gets to read the data.** Each agent in a run gets only the
permissions its job needs:

| | Main agent (your session) | `risk-analyst` subagent |
|---|---|---|
| Shell, file edits, network | Switched off during the run | Never had them |
| Your project files | Can read them | Cannot read them\* |
| The fetched release data | Cannot read it\* | The only thing it can read\* |
| The helper's `fetch` and `render` tools | Can call them | Cannot call them |

The starred cells describe one mechanism: a fence around the fetched data,
enforced by a hook the plugin ships and backed by a registry of the exact
directories the fetch step created. Read-only is not enough on its own: an
injected instruction could still make the AI read `~/.ssh` or a `.env`
file and leak what it finds into the report. The risk-analyst may read only
inside the fence, every other agent may read anywhere but there, and if
anything goes wrong with the registry the hook denies rather than allows.
The plugin cannot override your own Claude Code settings: anything you
have denied there stays denied. The result: a prompt injection buried in a
diff reaches only the risk-analyst, and can, at worst, change what the
analysis says. It cannot run commands in your session, and the hostile
text cannot be pulled into any other agent's context.

**What comes out, and what remains.** Every file is written by the helper,
and it refuses to overwrite anything that is not a previously generated
Soundings report. Recognizable credentials (platform tokens, cloud keys,
PEM blocks, JWTs) are redacted before the analysis is even validated.
Externally-authored text is escaped in the report, so a crafted comment
cannot forge its structure. The verdict is computed from the concern
severities, so flipping it would require fabricating or suppressing a
whole concern, in plain sight, in a report a human will read. And nothing
stays behind: a successful render deletes the fetched data, a failed fetch
cleans up behind itself, and an abandoned run's leftovers are deleted the
next time the helper starts. Only the report survives.

**The residual risk.** A prompt injection can still bias the analysis
wording or a concern's severity. That risk is inherent to any LLM-based
review, which is why every report opens with an advisory banner and why
the footer names the exact model that performed the analysis. The
severity-driven verdict at least forces that bias into the most auditable
place there is: a named concern, in a report, right where you will read it.

If you would rather verify all of this than trust it: the helper is a
small, tested, open-source Go program you can read in an afternoon, and it
never runs arbitrary commands. It runs as an MCP server that does nothing
until a tool is called (no credentials read, no network touched), and you
can toggle it off in the `/mcp` panel.

**Optional hardening.** As a coarse second net independent of Soundings,
deny rules in your Claude Code `settings.json` keep high-value secret paths
unreadable for the whole session, subagents included. Deny rules always
beat allow rules:

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

## Running unattended

A normal run needs no permission prompt, and the injection containment
does not rely on a human watching: the plugin's hook pre-approves the
risk-analyst's reads inside the fetch directory and the plugin's own
`fetch` and `render` tools, and no agent ever writes a file. Your own deny
and ask rules always win. If you run with the plugin's hooks disabled, add
`mcp__plugin_soundings_helper__fetch` and
`mcp__plugin_soundings_helper__render` to `permissions.allow` instead.

The only thing a headless run (`claude -p`) has to provide is the report
path, since nobody is there to answer the question; without one it stops
with a usage error before fetching anything. For containerized use, build
the helper binary into the image and point the MCP server configuration at
it instead of `go run`.
