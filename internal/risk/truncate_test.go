package risk

import (
	"strconv"
	"strings"
	"testing"
)

func linesOf(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line " + strconv.Itoa(i)
	}
	return strings.Join(lines, "\n")
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name          string
		tier          string
		lineCount     int
		wantTruncated bool
	}{
		{"critical is never truncated", "critical", 100000, false},
		{"unrecognized tier is never truncated", "", 100000, false},
		{"high under threshold is untouched", "high", 2000, false},
		{"high over threshold is truncated", "high", 2001, true},
		{"medium under threshold is untouched", "medium", 1000, false},
		{"medium over threshold is truncated", "medium", 1001, true},
		{"low under threshold is untouched", "low", 300, false},
		{"low over threshold is truncated", "low", 301, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := linesOf(tt.lineCount)
			got, truncated := Truncate(patch, tt.tier)
			if truncated != tt.wantTruncated {
				t.Fatalf("Truncate() truncated = %v, want %v", truncated, tt.wantTruncated)
			}
			if !truncated && got != patch {
				t.Fatalf("Truncate() modified an untruncated patch")
			}
		})
	}
}

func TestTruncateContent(t *testing.T) {
	patch := linesOf(301)
	got, truncated := Truncate(patch, "low")
	if !truncated {
		t.Fatalf("expected truncation")
	}
	if !strings.HasPrefix(got, "line 0\n") {
		t.Fatalf("expected patch to start with the first kept line, got: %q", got[:20])
	}
	if !strings.HasSuffix(got, "line 300") {
		t.Fatalf("expected patch to end with the last kept line, got: %q", got[len(got)-20:])
	}
	if !strings.Contains(got, "... [231 lines omitted] ...") {
		t.Fatalf("expected omission marker with correct count, got: %q", got)
	}
	// Content strictly between the kept head and tail must be gone.
	if strings.Contains(got, "line 150") {
		t.Fatalf("expected middle content to be omitted, but found it")
	}
}
