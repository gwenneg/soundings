package risk

import (
	"fmt"
	"strings"
)

// truncateThreshold maps a risk tier to the patch line count above which
// its patches get truncated. Critical-risk files (auth, DB, API contracts)
// have no entry: they are never truncated, however large, since the
// assessing agent always needs full visibility into them. Thresholds get
// tighter down the tier ladder because lower-risk files (generated code,
// vendored bundles, lockfiles) rarely need full visibility even when huge.
var truncateThreshold = map[string]int{
	"high":   2000,
	"medium": 1000,
	"low":    300,
}

// Lines kept from the start and end of a truncated patch. Chosen to be
// generous rather than aggressive: unlike RCS, which escalates through
// progressively tighter levels on a context-window retry, soundings applies
// truncation once at fetch time and has no retry loop to fall back on.
const (
	truncateKeepStart = 50
	truncateKeepEnd   = 20
)

// Truncate keeps the first and last lines of a patch and replaces the
// middle with a labeled omission marker, so a reader can tell content is
// missing rather than being shown a partial diff that looks complete. It
// returns the patch unchanged, and false, when the tier is exempt
// (critical, or unrecognized) or the patch doesn't exceed the tier's
// threshold.
func Truncate(patch, tier string) (string, bool) {
	threshold, ok := truncateThreshold[tier]
	if !ok {
		return patch, false
	}

	lines := strings.Split(patch, "\n")
	if len(lines) <= threshold {
		return patch, false
	}

	endsWithNewline := strings.HasSuffix(patch, "\n")

	var b strings.Builder
	for _, l := range lines[:truncateKeepStart] {
		b.WriteString(l)
		b.WriteString("\n")
	}

	omitted := len(lines) - truncateKeepStart - truncateKeepEnd
	fmt.Fprintf(&b, "\n... [%d lines omitted] ...\n\n", omitted)

	tail := lines[len(lines)-truncateKeepEnd:]
	for i, l := range tail {
		b.WriteString(l)
		if i < len(tail)-1 || endsWithNewline {
			b.WriteString("\n")
		}
	}

	return b.String(), true
}
