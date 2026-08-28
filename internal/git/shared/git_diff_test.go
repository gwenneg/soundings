package shared

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gwenneg/soundings/internal/git/types"
)

func TestShortSHA(t *testing.T) {
	tests := []struct {
		name     string
		sha      string
		expected string
	}{
		{"long sha", "abcdef1234567890", "abcdef12"},
		{"exactly 8 chars", "abcdef12", "abcdef12"},
		{"short sha", "abc", "abc"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShortSHA(tt.sha); got != tt.expected {
				t.Errorf("ShortSHA(%q) = %q, want %q", tt.sha, got, tt.expected)
			}
		})
	}
}

func TestParseNameStatusZ(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []nameStatusEntry
	}{
		{"empty", "", nil},
		{
			"added and modified",
			"A\x00new.go\x00M\x00existing.go\x00",
			[]nameStatusEntry{
				{Status: "A", NewPath: "new.go"},
				{Status: "M", NewPath: "existing.go"},
			},
		},
		{
			"rename",
			"R100\x00old.go\x00new.go\x00",
			[]nameStatusEntry{
				{Status: "R100", OldPath: "old.go", NewPath: "new.go"},
			},
		},
		{
			"delete",
			"D\x00gone.go\x00",
			[]nameStatusEntry{
				{Status: "D", NewPath: "gone.go"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseNameStatusZ([]byte(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("parseNameStatusZ(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSplitPatchesByFile(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n" +
		"index 111..222 100644\n" +
		"--- a/a.go\n" +
		"+++ b/a.go\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new\n" +
		"diff --git a/b.go b/b.go\n" +
		"index 333..444 100644\n" +
		"--- a/b.go\n" +
		"+++ b/b.go\n" +
		"@@ -1 +1 @@\n" +
		"-x\n" +
		"+y\n"

	sections := splitPatchesByFile([]byte(diff))
	if len(sections) != 2 {
		t.Fatalf("len(sections) = %d, want 2", len(sections))
	}
	if !strings.HasPrefix(sections[0], "diff --git a/a.go b/a.go") {
		t.Errorf("sections[0] doesn't start with the a.go header: %q", sections[0])
	}
	if !strings.HasPrefix(sections[1], "diff --git a/b.go b/b.go") {
		t.Errorf("sections[1] doesn't start with the b.go header: %q", sections[1])
	}

	if got := splitPatchesByFile([]byte("")); got != nil {
		t.Errorf("splitPatchesByFile(empty) = %+v, want nil", got)
	}
}

func TestStatusWord(t *testing.T) {
	tests := []struct{ code, want string }{
		{"A", "added"},
		{"D", "removed"},
		{"R100", "renamed"},
		{"C75", "copied"},
		{"M", "modified"},
		{"T", "modified"},
	}
	for _, tt := range tests {
		if got := statusWord(tt.code); got != tt.want {
			t.Errorf("statusWord(%q) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestParsePatchStats(t *testing.T) {
	tests := []struct {
		name              string
		patch             string
		expectedAdditions int
		expectedDeletions int
	}{
		{"empty patch", "", 0, 0},
		{"only additions", "+line1\n+line2\n+line3", 3, 0},
		{"only deletions", "-line1\n-line2", 0, 2},
		{
			"mixed changes",
			"@@ -1,5 +1,10 @@\n-old\n+new\n context\n-removed\n+added1\n+added2",
			3, 2,
		},
		{
			"skip diff headers",
			"--- a/file.go\n+++ b/file.go\n@@ -1,5 +1,10 @@\n-old\n+new",
			1, 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			additions, deletions := ParsePatchStats(tt.patch)
			if additions != tt.expectedAdditions {
				t.Errorf("additions = %d, want %d", additions, tt.expectedAdditions)
			}
			if deletions != tt.expectedDeletions {
				t.Errorf("deletions = %d, want %d", deletions, tt.expectedDeletions)
			}
		})
	}
}

func TestCalculateStats(t *testing.T) {
	files := []types.FileChange{
		{Additions: 2, Deletions: 0},
		{Additions: 0, Deletions: 3},
		{Additions: 1, Deletions: 1},
	}
	stats := CalculateStats(files)
	if stats.TotalFiles != 3 || stats.TotalAdditions != 3 || stats.TotalDeletions != 4 || stats.TotalChanges != 7 {
		t.Errorf("CalculateStats() = %+v", stats)
	}
}

func TestBasicAuthHeader(t *testing.T) {
	got := BasicAuthHeader("x-access-token", "secret")
	want := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:secret"))
	if got != want {
		t.Errorf("BasicAuthHeader() = %q, want %q", got, want)
	}
}

// TestFetchGitDiffEndToEnd exercises the real git binary against a local
// repository (no network, no auth) to confirm the clone/diff/parse pipeline
// produces correct results for modifications, additions, and renames.
func TestFetchGitDiffEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	srcDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = srcDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	revParse := func() string {
		t.Helper()
		out, err := exec.Command("git", "-C", srcDir, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatalf("rev-parse: %v", err)
		}
		return strings.TrimSpace(string(out))
	}

	run("init", "--quiet", "-b", "main")
	writeFile(t, srcDir, "a.txt", "line1\nline2\n")
	writeFile(t, srcDir, "b.txt", "new file\n")
	run("add", "a.txt", "b.txt")
	run("commit", "--quiet", "-m", "add a.txt and b.txt")
	base := revParse()

	writeFile(t, srcDir, "a.txt", "line1\nline2 modified\n")
	run("add", "a.txt")
	run("commit", "--quiet", "-m", "modify a.txt")

	run("mv", "b.txt", "c.txt")
	run("commit", "--quiet", "-m", "rename b.txt to c.txt")
	head := revParse()

	commits, files, err := FetchGitDiff(context.Background(), srcDir, CloneAuth{}, base, head)
	if err != nil {
		t.Fatalf("FetchGitDiff: %v", err)
	}

	if len(commits) != 2 {
		t.Fatalf("len(commits) = %d, want 2: %+v", len(commits), commits)
	}
	if commits[0].Message != "modify a.txt" {
		t.Errorf("commits[0].Message = %q", commits[0].Message)
	}
	if commits[1].Message != "rename b.txt to c.txt" {
		t.Errorf("commits[1].Message = %q", commits[1].Message)
	}

	byName := map[string]types.FileChange{}
	for _, f := range files {
		byName[f.Filename] = f
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2: %+v", len(files), files)
	}
	a := byName["a.txt"]
	if a.Status != "modified" || a.Additions != 1 || a.Deletions != 1 {
		t.Errorf("a.txt = %+v", a)
	}
	c := byName["c.txt"]
	if c.Status != "renamed" || c.PreviousFilename != "b.txt" {
		t.Errorf("c.txt = %+v", c)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}
