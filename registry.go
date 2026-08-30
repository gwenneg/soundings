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

	// Kind distinguishes directory entries (everything under them is
	// authorized; "" for compatibility with entries written before files
	// existed) from single-file entries ("file", exactly that path - used
	// for the report copy at report_path so post-render edits to it are
	// pre-approved).
	Kind string `json:"kind,omitempty"`
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

// registerDir records dir (absolute, symlink-resolved) as authorized,
// pruning dead and expired entries while it holds the file. The directory
// must exist.
func registerDir(dir string) error {
	return register(dir, "dir")
}

// registerFile records one file (absolute, symlink-resolved) as authorized,
// so the hook can pre-approve later Write/Edit calls on exactly that path.
// The file must exist.
func registerFile(file string) error {
	return register(file, "file")
}

func register(target, kind string) error {
	resolved, err := canonicalPath(target)
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
	kept = append(kept, registryEntry{Dir: resolved, RegisteredAt: time.Now().UTC(), Kind: kind})
	return saveEntries(path, kept)
}

// deregisterDir removes dir from the registry. A missing registry or a dir
// that was never registered is not an error - the goal state (not
// authorized) already holds.
func deregisterDir(dir string) error {
	resolved, err := canonicalPath(dir)
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

// allowedTargets returns the currently authorized directories and files
// for the hook. Read-only: expired or dead entries are filtered in memory
// but the registry file is only rewritten by register/deregister, so the
// hook never needs write access to the cache directory.
func allowedTargets() (dirs, files []string) {
	path, err := registryPath()
	if err != nil {
		return nil, nil
	}
	for _, e := range pruneEntries(loadEntries(path)) {
		if e.Kind == "file" {
			files = append(files, e.Dir)
		} else {
			dirs = append(dirs, e.Dir)
		}
	}
	return dirs, files
}

// canonicalPath makes a path absolute and symlink-resolved, so registry
// entries compare equal to the resolved form of any path the hook checks
// (e.g. a macOS /tmp/... path registering as /private/tmp/...).
func canonicalPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
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
		info, err := os.Stat(e.Dir)
		if err != nil {
			continue
		}
		if wantDir := e.Kind != "file"; info.IsDir() != wantDir {
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
