package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The registry records which fetch output directories are currently
// authorized for the risk-analyst agent's Read, Grep, and Glob tools.
// doFetch registers its output directory, a successful render deregisters
// it, and the PreToolUse hook (hook.go) allows reads only inside registered
// directories and denies everything else. Authorization is therefore tied
// to the directories this binary actually created, not to a naming
// convention an unrelated path could accidentally (or deliberately) match.
//
// Only the fetch helper ever writes files - fetched content cannot - so
// nothing attacker-influenced can add an entry. Every failure mode reads as
// "not registered", which the hook turns into a deny: the registry fails
// closed.

// registryTTL bounds how long an entry survives without being deregistered
// (a run abandoned mid-way, a crash before render). Generous on purpose:
// its only job is to stop crashed runs from staying authorized forever.
const registryTTL = 24 * time.Hour

type registryEntry struct {
	Dir          string    `json:"dir"`
	RegisteredAt time.Time `json:"registered_at"`
}

// registryPath returns the fixed per-user location of the registry file.
// SOUNDINGS_CACHE_DIR overrides the base directory (used by tests).
func registryPath() (string, error) {
	base := os.Getenv("SOUNDINGS_CACHE_DIR")
	if base == "" {
		d, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(d, "soundings")
	}
	return filepath.Join(base, "allowed-dirs.json"), nil
}

// registerDir records dir (absolute, symlink-resolved) as authorized for
// the risk-analyst agent, pruning dead and expired entries while it holds
// the file. The directory must exist.
func registerDir(dir string) error {
	resolved, err := canonicalDir(dir)
	if err != nil {
		return err
	}
	path, err := registryPath()
	if err != nil {
		return err
	}
	entries := pruneEntries(loadEntries(path))
	kept := entries[:0]
	for _, e := range entries {
		if e.Dir != resolved {
			kept = append(kept, e)
		}
	}
	kept = append(kept, registryEntry{Dir: resolved, RegisteredAt: time.Now().UTC()})
	return saveEntries(path, kept)
}

// deregisterDir removes dir from the registry. A missing registry or a dir
// that was never registered is not an error - the goal state (not
// authorized) already holds.
func deregisterDir(dir string) error {
	resolved, err := canonicalDir(dir)
	if err != nil {
		return err
	}
	path, err := registryPath()
	if err != nil {
		return err
	}
	entries := loadEntries(path)
	if entries == nil {
		return nil
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.Dir != resolved {
			kept = append(kept, e)
		}
	}
	return saveEntries(path, kept)
}

// allowedDirs returns the currently authorized directories for the hook.
// Read-only: expired or dead entries are filtered in memory but the file is
// only rewritten by register/deregister, so the hook never needs write
// access to the cache directory.
func allowedDirs() []string {
	path, err := registryPath()
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range pruneEntries(loadEntries(path)) {
		dirs = append(dirs, e.Dir)
	}
	return dirs
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

// loadEntries returns nil on any error: an unreadable or corrupt registry
// means nothing is authorized.
func loadEntries(path string) []registryEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entries []registryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil
	}
	return entries
}

func pruneEntries(entries []registryEntry) []registryEntry {
	kept := entries[:0]
	for _, e := range entries {
		if time.Since(e.RegisteredAt) > registryTTL {
			continue
		}
		if info, err := os.Stat(e.Dir); err != nil || !info.IsDir() {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

func saveEntries(path string, entries []registryEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// underDir reports whether path is dir itself or inside it. Both arguments
// must already be absolute and cleaned; the comparison is component-wise,
// so /tmp/soundings-abc-evil is not under /tmp/soundings-abc.
func underDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
