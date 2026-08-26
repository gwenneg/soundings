package github

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gwenneg/soundings/internal/git/types"

	"github.com/google/go-github/v90/github"
	"golang.org/x/sync/errgroup"
)

// fetchDiff fetches comparison data from GitHub and augments commits with PR metadata
// Returns a complete Comparison with augmented commits, files, and stats
// The cache parameter allows sharing cached PR objects across multiple operations
func fetchDiff(ctx context.Context, client *github.Client, owner, repo, base, head, diffURL string) (*types.Comparison, error) {
	slog.Debug("Starting comparison fetch and commit augmentation", "owner", owner, "repo", repo, "base", base, "head", head)

	// Fetch comparison data with all commits (handles pagination)
	ghComparison, allCommits, err := fetchComparisonWithPagination(ctx, client, owner, repo, base, head)
	if err != nil {
		return nil, err
	}

	slog.Debug("Fetched GitHub comparison", "commits", len(allCommits), "files", len(ghComparison.Files))

	// Initialize comparison with files and stats from GitHub
	comparison := &types.Comparison{
		Platform: "github",
		RepoURL:  fmt.Sprintf("https://github.com/%s/%s", owner, repo),
		DiffURL:  diffURL,
		Commits:  make([]types.Commit, len(allCommits)),
		Files:    convertFiles(ghComparison.Files),
		Stats:    calculateStats(ghComparison.Files),
	}

	// GitHub's compare API returns at most 300 files and offers no way to
	// page through the rest; flag it so the analysis knows the diff is partial.
	if len(ghComparison.Files) >= 300 {
		comparison.FilesMayBeTruncated = true
		slog.Warn("GitHub compare returned 300 files - the platform caps this list, so the diff may be incomplete",
			"owner", owner, "repo", repo)
	}

	// Process each commit for augmentation in parallel (PR number)
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(10) // Limit concurrent API calls to avoid rate limiting

	for i, commit := range allCommits {
		g.Go(func() error {
			comparison.Commits[i] = buildCommitEntry(gCtx, client, commit, owner, repo)
			return nil
		})
	}
	g.Wait()

	slog.Debug("Commit augmentation complete", "commit_entries", len(comparison.Commits))

	return comparison, nil
}

// buildCommitEntry creates a commit entry from a GitHub commit, resolving its PR number
func buildCommitEntry(ctx context.Context, client *github.Client, commit *github.RepositoryCommit, owner, repo string) types.Commit {
	entry := types.Commit{
		SHA:      commit.GetSHA(),
		ShortSHA: shortSHA(commit.GetSHA()),
		Message:  "No message",
		Author:   "Unknown",
	}

	// Extract commit message (first line only)
	if msg := commit.GetCommit().GetMessage(); msg != "" {
		entry.Message = strings.TrimSpace(strings.SplitN(msg, "\n", 2)[0])
	}

	// Extract author name
	if name := commit.GetCommit().GetAuthor().GetName(); name != "" {
		entry.Author = name
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
		return 0, fmt.Errorf("failed to find PRs for commit %s: %w", shortSHA(commitSHA), err)
	}

	slog.Debug("GitHub API response", "commit", shortSHA(commitSHA), "found_prs", len(prs), "rate_limit_remaining", resp.Rate.Remaining)

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

// fetchComparisonWithPagination fetches comparison data with full commit pagination
// GitHub API limits commits per page, so we need to paginate to get all commits
func fetchComparisonWithPagination(ctx context.Context, client *github.Client, owner, repo, base, head string) (*github.CommitsComparison, []*github.RepositoryCommit, error) {
	var allCommits []*github.RepositoryCommit
	var comparisonData *github.CommitsComparison
	opts := &github.ListOptions{Page: 1, PerPage: 100}

	for {
		comparison, resp, err := client.Repositories.CompareCommits(ctx, owner, repo, base, head, opts)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to fetch comparison from GitHub (page %d, owner=%s, repo=%s, base=%s, head=%s): %w",
				opts.Page, owner, repo, base, head, err)
		}

		// Store comparison data from first page
		if opts.Page == 1 {
			comparisonData = comparison
		}

		// Collect commits from this page
		if comparison.Commits != nil {
			allCommits = append(allCommits, comparison.Commits...)
		}

		// Check if we have more pages
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return comparisonData, allCommits, nil
}

// shortSHA returns the first 8 characters of a SHA, or the whole string if shorter.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// convertFiles converts GitHub CommitFiles to platform-agnostic FileChanges
func convertFiles(files []*github.CommitFile) []types.FileChange {
	if files == nil {
		return []types.FileChange{}
	}

	result := make([]types.FileChange, 0, len(files))
	for _, file := range files {
		result = append(result, convertFile(file))
	}
	return result
}

// convertFile converts a GitHub CommitFile to platform-agnostic FileChange
func convertFile(file *github.CommitFile) types.FileChange {
	if file == nil {
		return types.FileChange{}
	}
	return types.FileChange{
		Filename:         file.GetFilename(),
		Status:           file.GetStatus(),
		Additions:        file.GetAdditions(),
		Deletions:        file.GetDeletions(),
		Changes:          file.GetChanges(),
		Patch:            file.GetPatch(),
		PreviousFilename: file.GetPreviousFilename(),
	}
}

// calculateStats calculates comparison statistics from GitHub files
func calculateStats(files []*github.CommitFile) types.ComparisonStats {
	stats := types.ComparisonStats{
		TotalFiles: len(files),
	}

	for _, file := range files {
		if file == nil {
			continue
		}
		stats.TotalAdditions += file.GetAdditions()
		stats.TotalDeletions += file.GetDeletions()
	}

	stats.TotalChanges = stats.TotalAdditions + stats.TotalDeletions
	return stats
}
