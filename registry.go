package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The registry records which fetch output directories currently hold live,
// externally-authored fetch data; the PreToolUse hook (hook.go) reads it to
// confine Read/Grep/Glob in both directions. One file per entry
// (<cache>/soundings/allowed-dirs/<sha256(dir)>.json), so concurrent
// sessions never rewrite each other's entries - a dropped entry would
// silently unprotect on-disk untrusted content. Only the fetch helper ever
// writes files, so nothing attacker-influenced can add an entry, and every
// failure mode reads as "not registered": a deny for the risk-analyst
// (fail closed), but no fence for everyone else - a registry made
// unreadable out-of-band leaves surviving data unfenced, which is inherent
// to an external-state fence; the README's user deny rules are the net.

// registryTTL stops a crashed run from staying authorized forever. The
// register-time prune deletes an expired entry's directory along with the
// entry: registration and data share one lifetime, and every registered
// directory is helper-created, so the data has no other custodian.
const registryTTL = 24 * time.Hour

type registryEntry struct {
	Dir          string    `json:"dir"`
	RegisteredAt time.Time `json:"registered_at"`
}

// entriesDir returns the fixed per-user directory holding one file per
// registered fetch directory. SOUNDINGS_CACHE_DIR overrides the base
// directory (used by tests).
func entriesDir() (string, error) {
	base := os.Getenv("SOUNDINGS_CACHE_DIR")
	if base == "" {
		d, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(d, "soundings")
	}
	return filepath.Join(base, "allowed-dirs"), nil
}

// entryFile maps a canonical directory path to its entry file.
func entryFile(entries, canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return filepath.Join(entries, hex.EncodeToString(sum[:])+".json")
}

// registerDir records dir (absolute, symlink-resolved) as authorized for
// the risk-analyst agent and fenced off from everyone else, pruning
// expired and dead entries while it is here. The directory must exist.
func registerDir(dir string) error {
	resolved, err := canonicalDir(dir)
	if err != nil {
		return err
	}
	entries, err := entriesDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(entries, 0o700); err != nil {
		return err
	}

	data, err := json.Marshal(registryEntry{Dir: resolved, RegisteredAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	// Write-then-rename so a concurrent hook never reads a torn entry.
	tmp := entryFile(entries, resolved) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, entryFile(entries, resolved)); err != nil {
		return err
	}
	pruneEntries(entries)
	return nil
}

// releaseDir ends a fetch directory's life: deletion first, then the
// registration - so a failed deletion leaves the keep-out covering the
// leftovers until the TTL prune retries. Best-effort: a produced report
// outranks cleanup.
func releaseDir(dir string) {
	resolved, err := canonicalDir(dir)
	if err != nil {
		// The directory may already be gone; resolve what still exists of
		// the path so the entry file's hash matches what registerDir stored.
		abs, absErr := filepath.Abs(dir)
		if absErr != nil {
			slog.Warn("Failed to resolve fetch directory for release", "dir", dir, "error", err)
			return
		}
		resolved = resolveExisting(filepath.Clean(abs))
	}
	if err := os.RemoveAll(resolved); err != nil {
		slog.Warn("Failed to delete fetch data directory", "dir", resolved, "error", err)
		return
	}
	entries, err := entriesDir()
	if err != nil {
		return
	}
	if err := os.Remove(entryFile(entries, resolved)); err != nil && !os.IsNotExist(err) {
		slog.Warn("Failed to remove registry entry", "dir", resolved, "error", err)
	}
}

// lookupRegistered reports whether dir is a live registered fetch
// directory, returning its canonical form. Every failure reads as "not
// registered".
func lookupRegistered(dir string) (string, bool) {
	resolved, err := canonicalDir(dir)
	if err != nil {
		return "", false
	}
	entries, err := entriesDir()
	if err != nil {
		return "", false
	}
	e, ok := readEntry(entryFile(entries, resolved))
	if !ok || e.Dir != resolved || time.Since(e.RegisteredAt) > registryTTL {
		return "", false
	}
	return resolved, true
}

// registryDirs returns the hook's two views of the registry: authorized
// (entries within the TTL, the risk-analyst's read authorization) and
// fenced (every entry whose directory still exists, TTL ignored - the
// keep-out must hold as long as the data does; deletion, not the clock,
// ends it). Read-only, so the hook never needs write access to the cache.
func registryDirs() (authorized, fenced []string) {
	entries, err := entriesDir()
	if err != nil {
		return nil, nil
	}
	names, _ := os.ReadDir(entries)
	for _, n := range names {
		if n.IsDir() || !strings.HasSuffix(n.Name(), ".json") {
			continue
		}
		e, ok := readEntry(filepath.Join(entries, n.Name()))
		if !ok {
			continue
		}
		if info, err := os.Stat(e.Dir); err != nil || !info.IsDir() {
			continue
		}
		fenced = append(fenced, e.Dir)
		if time.Since(e.RegisteredAt) <= registryTTL {
			authorized = append(authorized, e.Dir)
		}
	}
	return authorized, fenced
}

// allowedDirs returns the risk-analyst's currently authorized directories.
func allowedDirs() []string {
	authorized, _ := registryDirs()
	return authorized
}

// fencedDirs returns every directory still holding live fetch data, the
// keep-out list for non-risk-analyst callers.
func fencedDirs() []string {
	_, fenced := registryDirs()
	return fenced
}

// readEntry returns false on any error: an unreadable or corrupt entry
// authorizes nothing.
func readEntry(path string) (registryEntry, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return registryEntry{}, false
	}
	var e registryEntry
	if err := json.Unmarshal(data, &e); err != nil || e.Dir == "" {
		return registryEntry{}, false
	}
	return e, true
}

// pruneEntries removes expired and dead entries, deleting an expired
// entry's directory along with it; if that deletion fails, the entry is
// kept and the next prune retries.
func pruneEntries(entries string) {
	names, err := os.ReadDir(entries)
	if err != nil {
		return
	}
	for _, n := range names {
		if n.IsDir() || !strings.HasSuffix(n.Name(), ".json") {
			continue
		}
		path := filepath.Join(entries, n.Name())
		e, ok := readEntry(path)
		switch {
		case !ok:
			os.Remove(path)
		case time.Since(e.RegisteredAt) > registryTTL:
			if err := os.RemoveAll(e.Dir); err != nil {
				slog.Warn("Failed to delete expired fetch data directory", "dir", e.Dir, "error", err)
				continue
			}
			os.Remove(path)
		default:
			if info, err := os.Stat(e.Dir); err != nil || !info.IsDir() {
				os.Remove(path)
			}
		}
	}
}

// canonicalDir makes dir absolute and symlink-resolved, so registry entries
// compare equal to the resolved form of any path the hook checks (e.g. a
// macOS /tmp/... path registering as /private/tmp/...).
func canonicalDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// underDir reports whether path is dir itself or inside it (both absolute
// and cleaned), comparing component-wise. fold makes the comparison
// case-insensitive - required in the DENY direction, where on a
// case-insensitive filesystem (the macOS default) /PRIVATE/tmp/x names the
// same directory as /private/tmp/x yet EvalSymlinks preserves the caller's
// case; folding must never be used in the ALLOW direction, where on a
// case-sensitive filesystem it would approve a genuinely distinct,
// attacker-creatable case-variant directory. Residual: no
// Unicode-normalization folding, which matters only for non-ASCII TMPDIRs.
func underDir(path, dir string, fold bool) bool {
	p, d := pathComponents(path), pathComponents(dir)
	if len(p) < len(d) {
		return false
	}
	for i := range d {
		if fold {
			if !strings.EqualFold(p[i], d[i]) {
				return false
			}
		} else if p[i] != d[i] {
			return false
		}
	}
	return true
}

func pathComponents(p string) []string {
	var out []string
	for _, s := range strings.Split(filepath.Clean(p), string(filepath.Separator)) {
		if s != "" && s != "." {
			out = append(out, s)
		}
	}
	return out
}
