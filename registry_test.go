package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegisterAndDeregisterRoundTrip(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dir := t.TempDir()

	if err := registerDir(dir); err != nil {
		t.Fatalf("registerDir: %v", err)
	}
	resolved, err := canonicalPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirs, _ := allowedTargets(); len(dirs) != 1 || dirs[0] != resolved {
		t.Fatalf("allowedTargets() = %v, want [%s]", dirs, resolved)
	}

	// Re-registering the same dir must not duplicate it.
	if err := registerDir(dir); err != nil {
		t.Fatalf("registerDir (again): %v", err)
	}
	if dirs, _ := allowedTargets(); len(dirs) != 1 {
		t.Fatalf("allowedTargets() after re-register = %v, want 1 entry", dirs)
	}

	if err := deregisterDir(dir); err != nil {
		t.Fatalf("deregisterDir: %v", err)
	}
	if dirs, _ := allowedTargets(); len(dirs) != 0 {
		t.Fatalf("allowedTargets() after deregister = %v, want empty", dirs)
	}
}

func TestRegisterFileRoundTrip(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	file := filepath.Join(t.TempDir(), "soundings-report.md")
	if err := os.WriteFile(file, []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := registerFile(file); err != nil {
		t.Fatalf("registerFile: %v", err)
	}
	resolved, err := canonicalPath(file)
	if err != nil {
		t.Fatal(err)
	}
	dirs, files := allowedTargets()
	if len(dirs) != 0 {
		t.Errorf("allowedTargets() dirs = %v, want empty", dirs)
	}
	if len(files) != 1 || files[0] != resolved {
		t.Fatalf("allowedTargets() files = %v, want [%s]", files, resolved)
	}

	// A file entry whose path is gone is pruned.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, files := allowedTargets(); len(files) != 0 {
		t.Fatalf("allowedTargets() files after removal = %v, want empty", files)
	}
}

func TestPruneDropsKindMismatch(t *testing.T) {
	// An entry recorded as a file but pointing at a directory (or vice
	// versa) is not honored.
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dir, err := canonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path, err := registryPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := saveEntries(path, []registryEntry{
		{Dir: dir, RegisteredAt: time.Now().UTC(), Kind: "file"},
	}); err != nil {
		t.Fatal(err)
	}
	dirs, files := allowedTargets()
	if len(dirs) != 0 || len(files) != 0 {
		t.Fatalf("allowedTargets() = (%v, %v), want empty (kind mismatch pruned)", dirs, files)
	}
}

func TestRegisterDirFailsForMissingDirectory(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	if err := registerDir(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("expected an error registering a directory that does not exist")
	}
}

func TestDeregisterDirWithoutRegistryIsNotAnError(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	if err := deregisterDir(t.TempDir()); err != nil {
		t.Fatalf("deregisterDir with no registry: %v", err)
	}
}

func TestAllowedDirsPrunesDeadAndExpiredEntries(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("SOUNDINGS_CACHE_DIR", cache)

	gone := filepath.Join(t.TempDir(), "soundings-gone")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := registerDir(gone); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	expired := t.TempDir()
	expiredResolved, err := canonicalPath(expired)
	if err != nil {
		t.Fatal(err)
	}
	path, err := registryPath()
	if err != nil {
		t.Fatal(err)
	}
	entries := append(loadEntries(path), registryEntry{
		Dir:          expiredResolved,
		RegisteredAt: time.Now().UTC().Add(-registryTTL - time.Hour),
	})
	if err := saveEntries(path, entries); err != nil {
		t.Fatal(err)
	}

	if dirs, _ := allowedTargets(); len(dirs) != 0 {
		t.Fatalf("allowedTargets() = %v, want empty (dead and expired entries pruned)", dirs)
	}
}

func TestAllowedDirsEmptyOnCorruptRegistry(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("SOUNDINGS_CACHE_DIR", cache)
	if err := os.WriteFile(filepath.Join(cache, "allowed-dirs.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dirs, _ := allowedTargets(); len(dirs) != 0 {
		t.Fatalf("allowedTargets() on corrupt registry = %v, want empty", dirs)
	}
}

func TestUnderDir(t *testing.T) {
	cases := []struct {
		path, dir string
		want      bool
	}{
		{"/tmp/soundings-a", "/tmp/soundings-a", true},
		{"/tmp/soundings-a/index.json", "/tmp/soundings-a", true},
		{"/tmp/soundings-a/patches/x/y.patch", "/tmp/soundings-a", true},
		{"/tmp/soundings-a-evil", "/tmp/soundings-a", false},
		{"/tmp/soundings-a-evil/f", "/tmp/soundings-a", false},
		{"/tmp", "/tmp/soundings-a", false},
		{"/etc/passwd", "/tmp/soundings-a", false},
	}
	for _, c := range cases {
		if got := underDir(c.path, c.dir); got != c.want {
			t.Errorf("underDir(%q, %q) = %v, want %v", c.path, c.dir, got, c.want)
		}
	}
}
