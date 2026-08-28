package gitlab

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gwenneg/soundings/internal/config"
	"github.com/gwenneg/soundings/internal/git/shared"
	"github.com/gwenneg/soundings/internal/git/types"

	gitlab "gitlab.com/gitlab-org/api/client-go/v2"
	"golang.org/x/sync/errgroup"
)

// fetchDiff computes the comparison's commit list and per-file diff via a
// local git clone (see shared.FetchGitDiff), then augments each commit with
// its MR number via the GitLab API - git has no notion of merge requests, so
// that part still requires the API client.
func fetchDiff(ctx context.Context, client *gitlab.Client, cfg *config.Config, host, projectPath, base, head, diffURL string) (*types.Comparison, error) {
	slog.Debug("Starting comparison fetch and commit augmentation", "project", projectPath, "base", base, "head", head)

	cloneURL := fmt.Sprintf("https://%s/%s.git", host, projectPath)
	var auth shared.CloneAuth
	if cfg.GitLabToken != "" {
		auth.Header = shared.BasicAuthHeader("oauth2", cfg.GitLabToken)
	}

	rawCommits, files, err := shared.FetchGitDiff(ctx, cloneURL, auth, base, head)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch diff via git: %w", err)
	}

	slog.Debug("Fetched diff via git", "commits", len(rawCommits), "files", len(files))

	comparison := &types.Comparison{
		Platform: "gitlab",
		RepoURL:  fmt.Sprintf("https://%s/%s", host, projectPath),
		DiffURL:  diffURL,
		Commits:  make([]types.Commit, len(rawCommits)),
		Files:    files,
		Stats:    shared.CalculateStats(files),
	}

	// Process each commit for augmentation in parallel (MR number)
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(10) // Limit concurrent API calls to avoid rate limiting

	for i, rc := range rawCommits {
		g.Go(func() error {
			comparison.Commits[i] = buildCommitEntry(gCtx, client, rc, projectPath)
			return nil
		})
	}
	g.Wait()

	slog.Debug("Commit augmentation complete", "commit_entries", len(comparison.Commits))

	return comparison, nil
}

// buildCommitEntry turns a git-log-sourced commit into a types.Commit,
// resolving its MR number via the GitLab API.
func buildCommitEntry(ctx context.Context, client *gitlab.Client, rc shared.RawCommit, projectPath string) types.Commit {
	entry := types.Commit{
		SHA:      rc.SHA,
		ShortSHA: rc.ShortSHA,
		Message:  rc.Message,
		Author:   rc.Author,
	}
	if entry.Message == "" {
		entry.Message = "No message"
	}
	if entry.Author == "" {
		entry.Author = "Unknown"
	}

	// Find MR for this commit
	mrIID, err := getMRForCommit(ctx, client, projectPath, entry.SHA)
	if err != nil {
		slog.Warn("Failed to find MR for commit", "commit", entry.ShortSHA, "error", err)
		return entry
	}

	if mrIID == 0 {
		slog.Debug("No MR found for commit", "commit", entry.ShortSHA)
		return entry
	}

	slog.Debug("Found MR for commit", "commit", entry.ShortSHA, "mr", mrIID)
	entry.PRNumber = mrIID

	return entry
}

func getMRForCommit(ctx context.Context, client *gitlab.Client, projectPath, commitSHA string) (int64, error) {
	mrs, _, err := client.Commits.ListMergeRequestsByCommit(projectPath, commitSHA, gitlab.WithContext(ctx))
	if err != nil {
		return 0, fmt.Errorf("failed to get MRs for commit %s: %w", shortSHA(commitSHA), err)
	}

	slog.Debug("GitLab API response", "commit", shortSHA(commitSHA), "found_mrs", len(mrs))

	// Find first merged MR
	for _, mr := range mrs {
		if mr.State == "merged" {
			return mr.IID, nil
		}
	}

	return 0, nil
}

// shortSHA returns the first 8 characters of a SHA, or the whole string if shorter.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
