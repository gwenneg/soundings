# Troubleshooting

| Symptom | What to do |
|---------|------------|
| `GitHub authentication unavailable` or `GitLab authentication unavailable for <host>` | Set the env var, or run `gh auth login` / `glab auth login --hostname <host>`. |
| A DNS or network timeout on an internal GitLab host | Usually the VPN is down, not an auth problem. |
| `unsupported compare URL` | Use a github.com `/compare/a...b` or GitLab `/-/compare/a...b` URL with a three-dot range. GitHub Enterprise Server hosts are not supported. |
| The skill says the `risk-analyst` agent is unavailable | You are running from a repository checkout rather than the installed plugin. Install it from the marketplace. |
| `refusing to overwrite ...: it exists and is not a previously generated soundings report` | The helper only overwrites its own reports. Choose another path. |
| A read is denied because it "may reach inside a registered fetch data directory" | The fence is working: only the risk-analyst may read fetched data. Wait for the run to finish, or inspect the files outside Claude Code. |
| The first run is slow to start | `go run` is downloading and compiling the helper's dependencies. Later runs use the cache. |

Set `SOUNDINGS_LOG_LEVEL=debug` in the environment Claude Code starts from
to see the helper's log.
