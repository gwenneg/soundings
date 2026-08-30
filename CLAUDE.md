# CLAUDE.md

## Commit messages

Every commit message must follow [Conventional Commits](https://www.conventionalcommits.org/)
(`type(scope): description`, e.g. `feat: add x`, `fix(render): handle y`).
Releasing runs through claude-ichiba's reusable workflow, which parses
commit subjects since the last tag to pick the next version: any `feat`
bumps minor, a `!` breaking marker bumps major, anything else bumps
patch. A non-conventional subject falls through to a patch bump instead
of the version it should actually trigger.
