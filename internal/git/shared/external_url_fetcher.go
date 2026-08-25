package shared

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	httputil "github.com/gwenneg/soundings/internal/http"
)

const httpTimeout = 30 * time.Second

// maxResponseBodySize caps the amount of data read from external URLs (1 MiB).
const maxResponseBodySize = 1 << 20

// fetchExternalURL fetches content from an external URL
func (d *DocumentationFetcher) fetchExternalURL(ctx context.Context, urlStr string) (string, error) {
	// Determine if this is a GitLab URL for token-forwarding decisions
	gitlabHost := gitlabHostname(d.config.GitLabBaseURL)
	isGitLab := isGitLabURL(urlStr, gitlabHost)

	httpClient := httputil.NewHTTPClient(httputil.HTTPClientOptions{
		Timeout:         httpTimeout,
		BlockPrivateIPs: true,
		// The configured GitLab instance is a trusted destination that may
		// legitimately live on a private IP; anything else comes from
		// repo-controlled content and must not reach internal infrastructure.
		// This is checked per dial, so a redirect away from GitLab to another
		// private host is still blocked.
		AllowedPrivateHost: gitlabHost,
	})

	// Go's redirect logic strips Authorization/Cookie on cross-host redirects
	// but knows nothing about PRIVATE-TOKEN. Strip it ourselves on any hop
	// whose host is not the trusted GitLab instance, so a repo-controlled URL
	// that redirects off-host cannot capture the token.
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if !isGitLabURL(req.URL.String(), gitlabHost) {
			req.Header.Del("PRIVATE-TOKEN")
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Add GitLab authentication if needed
	if isGitLab && d.config.GitLabToken != "" {
		req.Header.Set("PRIVATE-TOKEN", d.config.GitLabToken)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	limitedReader := io.LimitReader(resp.Body, maxResponseBodySize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if len(body) > maxResponseBodySize {
		return "", fmt.Errorf("response body exceeds %d MiB limit", maxResponseBodySize>>20)
	}

	return string(body), nil
}

// isGitLabURL checks if a URL's host exactly matches gitlabHost (as returned
// by gitlabHostname). Only exact hostname matches are accepted to prevent
// token leakage to attacker-controlled hosts.
func isGitLabURL(urlStr, gitlabHost string) bool {
	if gitlabHost == "" {
		return false
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil || parsedURL.Hostname() == "" {
		return false
	}

	return strings.ToLower(parsedURL.Hostname()) == gitlabHost
}

// gitlabHostname returns the lowercase hostname of the configured GitLab
// instance, or "" if unset or unparsable.
func gitlabHostname(gitlabBaseURL string) string {
	parsedBaseURL, err := url.Parse(gitlabBaseURL)
	if err != nil || parsedBaseURL.Hostname() == "" {
		return ""
	}

	return strings.ToLower(parsedBaseURL.Hostname())
}
