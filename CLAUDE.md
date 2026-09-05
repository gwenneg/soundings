# CLAUDE.md

## Commit messages

Every commit message must follow [Conventional Commits](https://www.conventionalcommits.org/)
(`type(scope): description`, e.g. `feat: add x`, `fix(render): handle y`).
Releasing runs through claude-ichiba's reusable workflow, which parses
commit subjects since the last tag to pick the next version: any `feat`
bumps minor, a `!` breaking marker bumps major, anything else bumps
patch. A non-conventional subject falls through to a patch bump instead
of the version it should actually trigger.

## Documentation follows the code

The docs describe the plugin as it is, not as it was. Any change to the
plugin's implementation or behavior (skills, hooks, agents, MCP tools,
report format, scoring, configuration, CLI flags) must land in the same
change as the matching update to `README.md`, `docs/DEMO_REPORT.md`, and
every other document that describes the affected behavior. Never leave
the docs for a follow-up: a change is not done until the documentation
reflects it. Before finishing, reread the docs touched by the change and
confirm that nothing they say has been made wrong by it.
