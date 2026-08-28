package types

import (
	"time"
)

// Comparison represents a git comparison between two refs, platform-agnostic
// Combines both raw diff data (files, stats) and augmented commit metadata (SHA, PR#)
type Comparison struct {
	Platform string          `json:"platform"` // "github" or "gitlab"
	RepoURL  string          `json:"repo_url"` // Repository URL (e.g., "https://github.com/owner/repo")
	DiffURL  string          `json:"diff_url"` // Direct link to the comparison/diff
	Commits  []Commit        `json:"commits"`  // Commits in this comparison with full metadata
	Files    []FileChange    `json:"files"`    // Files changed in this comparison
	Stats    ComparisonStats `json:"stats"`    // Statistics about the comparison
}

// ComparisonStats represents statistics about the comparison
type ComparisonStats struct {
	TotalFiles     int `json:"total_files"`
	TotalAdditions int `json:"total_additions"`
	TotalDeletions int `json:"total_deletions"`
	TotalChanges   int `json:"total_changes"`
}

// Commit represents a single commit with augmented metadata
type Commit struct {
	SHA      string `json:"sha"`       // Full commit SHA
	ShortSHA string `json:"short_sha"` // Short SHA for display
	Message  string `json:"message"`   // Commit message (first line only)
	Author   string `json:"author"`    // Author name
	PRNumber int64  `json:"pr_number"` // Associated PR/MR number (0 if none)
}

// FileChange represents a file that was changed in a comparison
type FileChange struct {
	Filename         string `json:"filename"`
	Status           string `json:"status"` // added, modified, removed, renamed
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Changes          int    `json:"changes"`
	Patch            string `json:"patch,omitempty"`
	PreviousFilename string `json:"previous_filename,omitempty"` // For renames
}

// Repository represents basic repository information
type Repository struct {
	Platform      string `json:"platform"` // "github" or "gitlab"
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
	URL           string `json:"url"`
}

// Documentation represents repository documentation
type Documentation struct {
	MainDocContent        string            `json:"main_doc_content,omitempty"`
	MainDocFile           string            `json:"main_doc_file,omitempty"`
	AdditionalDocsContent map[string]string `json:"additional_docs_content,omitempty"` // Successfully fetched linked docs: display name -> content
	AdditionalDocsOrder   []string          `json:"additional_docs_order,omitempty"`   // Order of successfully fetched docs
	FailedAdditionalDocs  map[string]string `json:"failed_additional_docs,omitempty"`  // Failed linked docs: display name -> error message
	Repository            Repository        `json:"repository"`

	// FetchError is set when the main documentation lookup failed for a
	// reason other than the file not existing (auth failure, network error,
	// server error) — "docs unavailable" rather than "docs absent".
	FetchError string `json:"fetch_error,omitempty"`
}

// UserGuidance represents a complete user guidance with metadata for reporting
type UserGuidance struct {
	Content      string    `json:"content"`       // The actual guidance content
	Author       string    `json:"author"`        // Platform username who posted it
	Date         time.Time `json:"date"`          // When it was posted
	CommentURL   string    `json:"comment_url"`   // Direct link to the comment
	IsAuthorized bool      `json:"is_authorized"` // Whether the author had permission to post
}
