// Package config holds the settings the git providers need. The helper CLI
// constructs Config directly, resolving tokens per platform and per GitLab
// host (env var, then gh/glab CLI fallback) — there is no env-var loading
// layer here.
package config

type Config struct {
	GitHubToken string

	// GitLabBaseURL is the base URL ("https://<host>") of the GitLab
	// instance this Config's client talks to. One Config exists per GitLab
	// host in a run; the token below is only ever sent to this host.
	GitLabBaseURL string

	GitLabToken string
}
