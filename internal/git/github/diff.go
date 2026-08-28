package github

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gwenneg/soundings/internal/config"
	"github.com/gwenneg/soundings/internal/git/shared"
	"github.com/gwenneg/soundings/internal/git/types"

	"github.com/google/go-github/v90/github"
	"golang.org/x/sync/errgroup"
)

// fetchDiff computes the comparison's commit list and per-file diff via a
// local git clone (see shared.FetchGitDiff), then augments each commit with
// its PR number via the GitHub API - git has no notion of pull requests, so
// that part still requires the API client.
func fetchDiff(ctx context.Context, client *github.Client, cfg *config.Config, owner, repo, base, head, diffURL string) (*types.Comparison, error) {
	slog.Debug("Starting comparison fetch and commit augmentation", "owner", owner, "repo", repo, "base", base, "head", head)

	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	var auth shared.CloneAuth
	if cfg.GitHubToken != "" {
		auth.Header = shared.BasicAuthHeader("x-access-token", cfg.GitHubToken)
	}

	rawCommits, files, err := shared.FetchGitDiff(ctx, cloneURL, auth, base, head)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch diff via git: %w", err)
	}

	slog.Debug("Fetched diff via git", "commits", len(rawCommits), "files", len(files))

	comparison := &types.Comparison{
		Platform: "github",
		RepoURL:  fmt.Sprintf("https://github.com/%s/%s", owner, repo),
		DiffURL:  diffURL,
		Commits:  make([]types.Commit, len(rawCommits)),
		Files:    files,
		Stats:    shared.CalculateStats(files),
	}

	// Process each commit for augmentation in parallel (PR number)
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(10) // Limit concurrent API calls to avoid rate limiting

	for i, rc := range rawCommits {
		g.Go(func() error {
			comparison.Commits[i] = buildCommitEntry(gCtx, client, rc, owner, repo)
			return nil
		})
	}
	g.Wait()

	slog.Debug("Commit augmentation complete", "commit_entries", len(comparison.Commits))

	return comparison, nil
}

// buildCommitEntry turns a git-log-sourced commit into a types.Commit,
// resolving its PR number via the GitHub API.
func buildCommitEntry(ctx context.Context, client *github.Client, rc shared.RawCommit, owner, repo string) types.Commit {
	entry := types.Commit{
		SHA:      rc.SHA,
		ShortSHA: rc.ShortSHA,
		Message:  rc.Message,
		Author:   rc.Author,
	}

	// Find PR for this commit
	prNumber, err := getPRForCommit(ctx, client, owner, repo, entry.SHA)
	if err != nil {
		slog.Warn("Failed to find PR for commit", "commit", entry.ShortSHA, "error", err)
		return entry
	}

	if prNumber == 0 {
		slog.Debug("No PR found for commit", "commit", entry.ShortSHA)
		return entry
	}

	slog.Debug("Found PR for commit", "commit", entry.ShortSHA, "pr", prNumber)
	entry.PRNumber = int64(prNumber)

	return entry
}

func getPRForCommit(ctx context.Context, client *github.Client, owner, repo, commitSHA string) (int, error) {
	prs, resp, err := client.PullRequests.ListPullRequestsWithCommit(ctx, owner, repo, commitSHA, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to find PRs for commit %s: %w", shared.ShortSHA(commitSHA), err)
	}

	slog.Debug("GitHub API response", "commit", shared.ShortSHA(commitSHA), "found_prs", len(prs), "rate_limit_remaining", resp.Rate.Remaining)

	// Find the first merged PR that belongs to the analyzed repository.
	// ListPullRequestsWithCommit returns PRs from the whole fork network, so
	// a commit in a fork carries the upstream repo's PR numbers - those must
	// not be resolved against this repo (wrong links, 404s on lookup).
	fullName := owner + "/" + repo
	for _, pr := range prs {
		if !pr.GetMergedAt().IsZero() && strings.EqualFold(pr.GetBase().GetRepo().GetFullName(), fullName) {
			return pr.GetNumber(), nil
		}
	}

	return 0, nil
}
