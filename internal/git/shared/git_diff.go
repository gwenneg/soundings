// Package shared: git-backed diff fetching.
//
// The commit list and per-file patches for a comparison come from cloning
// the repository and running `git diff`/`git log` locally, not from a
// platform's REST "compare" API - it's the same diff a human would see
// running the command themselves, with no host-imposed cap on file count or
// per-file patch size.
package shared

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/gwenneg/soundings/internal/git/types"
)

// RawCommit is a commit as read directly from git, before platform-specific
// PR/MR-number resolution is layered on by the caller.
type RawCommit struct {
	SHA      string
	ShortSHA string
	Author   string
	Message  string
}

// CloneAuth carries the HTTP header git should send while cloning. The
// value is injected via environment variables (see gitRunner.run), never as
// a command-line argument, so the token is never visible to other processes
// on the machine (e.g. via `ps aux`).
type CloneAuth struct {
	Header string // e.g. "Authorization: Basic <base64>"; empty means no auth
}

// BasicAuthHeader builds an HTTP Basic auth header value for the given
// username/token pair, suitable for CloneAuth.Header.
func BasicAuthHeader(username, token string) string {
	return "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+token))
}

// FetchGitDiff clones cloneURL into a temporary, self-cleaning directory and
// computes the commit list and per-file diff between base and head using the
// local git binary. The clone is removed before this function returns;
// nothing about it persists.
func FetchGitDiff(ctx context.Context, cloneURL string, auth CloneAuth, base, head string) ([]RawCommit, []types.FileChange, error) {
	dir, err := os.MkdirTemp("", "soundings-git-clone-")
	if err != nil {
		return nil, nil, fmt.Errorf("creating clone directory: %w", err)
	}
	defer os.RemoveAll(dir)

	r := &gitRunner{dir: dir, auth: auth}

	// No --filter/--depth: a plain clone works against every git host
	// (including older or self-hosted GitLab/GitHub Enterprise instances
	// that may not support partial clone), and --no-checkout skips writing
	// a working tree we don't need for diffing.
	if _, err := r.run(ctx, "clone", "--quiet", "--no-checkout", cloneURL, "."); err != nil {
		return nil, nil, fmt.Errorf("cloning repository: %w", err)
	}

	commits, err := r.commits(ctx, base, head)
	if err != nil {
		return nil, nil, err
	}
	files, err := r.files(ctx, base, head)
	if err != nil {
		return nil, nil, err
	}
	return commits, files, nil
}

// gitRunner runs git against one clone directory.
type gitRunner struct {
	dir  string
	auth CloneAuth
}

func (r *gitRunner) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.dir
	// GIT_CONFIG_* env vars (not `-c` flags) so the auth header - which
	// carries the token - never appears in this process's argv, where any
	// other process on the machine could read it via `ps`.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if r.auth.Header != "" {
		cmd.Env = append(cmd.Env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.extraHeader",
			"GIT_CONFIG_VALUE_0="+r.auth.Header,
		)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// commits lists the commits head has that base doesn't, oldest first -
// matching the order GitHub/GitLab's compare APIs returned.
func (r *gitRunner) commits(ctx context.Context, base, head string) ([]RawCommit, error) {
	out, err := r.run(ctx, "log", "--reverse", "--no-color", "--format=%H%x1f%h%x1f%an%x1f%s", base+".."+head)
	if err != nil {
		return nil, fmt.Errorf("listing commits: %w", err)
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil, nil
	}
	var commits []RawCommit
	for _, line := range strings.Split(trimmed, "\n") {
		parts := strings.SplitN(line, "\x1f", 4)
		if len(parts) != 4 {
			continue
		}
		commits = append(commits, RawCommit{SHA: parts[0], ShortSHA: parts[1], Author: parts[2], Message: parts[3]})
	}
	return commits, nil
}

// files computes the per-file diff between base and head using the same
// merge-base-relative semantics ("triple dot") GitHub/GitLab's compare UIs
// use, so changes made independently on base after the branches diverged
// don't show up as spurious deletions in the diff.
func (r *gitRunner) files(ctx context.Context, base, head string) ([]types.FileChange, error) {
	rangeSpec := base + "..." + head

	statusOut, err := r.run(ctx, "diff", "--find-renames", "-M", "--name-status", "-z", rangeSpec)
	if err != nil {
		return nil, fmt.Errorf("listing changed files: %w", err)
	}
	entries := parseNameStatusZ(statusOut)

	patchOut, err := r.run(ctx, "diff", "--find-renames", "-M", "--no-color", rangeSpec)
	if err != nil {
		return nil, fmt.Errorf("computing diff: %w", err)
	}
	patches := splitPatchesByFile(patchOut)

	if len(entries) != len(patches) {
		return nil, fmt.Errorf("internal error: %d changed files but %d diff sections - git diff output did not line up between invocations", len(entries), len(patches))
	}

	files := make([]types.FileChange, 0, len(entries))
	for i, e := range entries {
		patch := patches[i]
		additions, deletions := ParsePatchStats(patch)
		files = append(files, types.FileChange{
			Filename:         e.NewPath,
			PreviousFilename: e.OldPath,
			Status:           statusWord(e.Status),
			Patch:            patch,
			Additions:        additions,
			Deletions:        deletions,
			Changes:          additions + deletions,
		})
	}
	return files, nil
}

type nameStatusEntry struct {
	Status  string
	OldPath string
	NewPath string
}

// parseNameStatusZ parses `git diff --name-status -z` output: a flat stream
// of NUL-terminated fields. Ordinary entries are two fields (status, path);
// renames/copies are three (statusScore, oldPath, newPath) - NUL-delimited
// so filenames with spaces or unusual characters can't be misread.
func parseNameStatusZ(out []byte) []nameStatusEntry {
	trimmed := strings.TrimRight(string(out), "\x00")
	if trimmed == "" {
		return nil
	}
	tokens := strings.Split(trimmed, "\x00")
	var entries []nameStatusEntry
	for i := 0; i < len(tokens); {
		status := tokens[i]
		i++
		switch {
		case i >= len(tokens):
			return entries
		case strings.HasPrefix(status, "R"), strings.HasPrefix(status, "C"):
			if i+1 >= len(tokens) {
				return entries
			}
			entries = append(entries, nameStatusEntry{Status: status, OldPath: tokens[i], NewPath: tokens[i+1]})
			i += 2
		default:
			entries = append(entries, nameStatusEntry{Status: status, NewPath: tokens[i]})
			i++
		}
	}
	return entries
}

// splitPatchesByFile splits the combined output of `git diff` into one
// unified-diff section per file. Every file section starts with a line
// literally beginning "diff --git ", which git never emits mid-section.
func splitPatchesByFile(diff []byte) []string {
	if len(bytes.TrimSpace(diff)) == 0 {
		return nil
	}
	lines := strings.Split(string(diff), "\n")
	var sections []string
	var current []string
	flush := func() {
		if len(current) > 0 {
			sections = append(sections, strings.TrimRight(strings.Join(current, "\n"), "\n")+"\n")
			current = nil
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
		}
		current = append(current, line)
	}
	flush()
	return sections
}

// statusWord maps a `git diff --name-status` code to the platform-agnostic
// vocabulary types.FileChange.Status already uses (matching what GitHub's
// API returned for the same states).
func statusWord(code string) string {
	switch code[0] {
	case 'A':
		return "added"
	case 'D':
		return "removed"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	default: // M (modified), T (type change), U (unmerged)
		return "modified"
	}
}

// ParsePatchStats counts additions and deletions in a unified diff patch by
// counting lines that start with '+' or '-', excluding the "+++ "/"--- "
// file-header lines.
func ParsePatchStats(patch string) (additions, deletions int) {
	if patch == "" {
		return 0, 0
	}
	for _, line := range strings.Split(patch, "\n") {
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case '+':
			if !strings.HasPrefix(line, "+++ ") {
				additions++
			}
		case '-':
			if !strings.HasPrefix(line, "--- ") {
				deletions++
			}
		}
	}
	return additions, deletions
}

// CalculateStats sums per-file additions/deletions into comparison-wide
// totals. Platform-agnostic: both fetchers produce types.FileChange before
// calling this.
func CalculateStats(files []types.FileChange) types.ComparisonStats {
	stats := types.ComparisonStats{TotalFiles: len(files)}
	for _, f := range files {
		stats.TotalAdditions += f.Additions
		stats.TotalDeletions += f.Deletions
	}
	stats.TotalChanges = stats.TotalAdditions + stats.TotalDeletions
	return stats
}
