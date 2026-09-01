package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeEntry plants a registry entry directly, for expiry scenarios the
// public API can't produce.
func writeEntry(t *testing.T, dir string, registeredAt time.Time) {
	t.Helper()
	entries, err := entriesDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(entries, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(registryEntry{Dir: dir, RegisteredAt: registeredAt})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryFile(entries, dir), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterAndReleaseRoundTrip(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dir := t.TempDir()

	if err := registerDir(dir); err != nil {
		t.Fatalf("registerDir: %v", err)
	}
	resolved, err := canonicalDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirs := allowedDirs(); len(dirs) != 1 || dirs[0] != resolved {
		t.Fatalf("allowedDirs() = %v, want [%s]", dirs, resolved)
	}
	if canon, ok := lookupRegistered(dir); !ok || canon != resolved {
		t.Fatalf("lookupRegistered() = (%q, %v), want (%q, true)", canon, ok, resolved)
	}

	// Re-registering the same dir must not duplicate it.
	if err := registerDir(dir); err != nil {
		t.Fatalf("registerDir (again): %v", err)
	}
	if dirs := allowedDirs(); len(dirs) != 1 {
		t.Fatalf("allowedDirs() after re-register = %v, want 1 entry", dirs)
	}

	// Release deletes the data and the registration together.
	releaseDir(dir)
	if dirs := allowedDirs(); len(dirs) != 0 {
		t.Fatalf("allowedDirs() after release = %v, want empty", dirs)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("release must delete the directory (stat err=%v)", err)
	}
	if _, ok := lookupRegistered(dir); ok {
		t.Fatal("lookupRegistered() must report false after release")
	}
}

func TestRegisterDirFailsForMissingDirectory(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	if err := registerDir(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("expected an error registering a directory that does not exist")
	}
}

func TestReleaseDirOfUnregisteredDirectory(t *testing.T) {
	// The failed-fetch path: content exists, registration never happened.
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	dir := t.TempDir()

	releaseDir(dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("release must delete an unregistered directory too (stat err=%v)", err)
	}
}

func TestRegisterPrunesExpiredEntryAndItsData(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())

	abandoned := t.TempDir()
	abandonedResolved, err := canonicalDir(abandoned)
	if err != nil {
		t.Fatal(err)
	}
	writeEntry(t, abandonedResolved, time.Now().UTC().Add(-registryTTL-time.Hour))

	fresh := t.TempDir()
	if err := registerDir(fresh); err != nil {
		t.Fatal(err)
	}

	// Registration and data share one lifetime: the expired entry is gone
	// AND its abandoned directory was deleted with it.
	freshResolved, err := canonicalDir(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if dirs := allowedDirs(); len(dirs) != 1 || dirs[0] != freshResolved {
		t.Fatalf("allowedDirs() = %v, want [%s]", dirs, freshResolved)
	}
	if _, err := os.Stat(abandonedResolved); !os.IsNotExist(err) {
		t.Fatalf("the expired entry's directory must be deleted by the prune (stat err=%v)", err)
	}
}

func TestAllowedDirsSkipsDeadAndExpiredEntries(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())

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
	expiredResolved, err := canonicalDir(expired)
	if err != nil {
		t.Fatal(err)
	}
	writeEntry(t, expiredResolved, time.Now().UTC().Add(-registryTTL-time.Hour))

	if dirs := allowedDirs(); len(dirs) != 0 {
		t.Fatalf("allowedDirs() = %v, want empty (dead and expired entries skipped)", dirs)
	}

	// The keep-out outlives the authorization: an expired entry whose
	// directory still exists stays fenced until the prune deletes both.
	if dirs := fencedDirs(); len(dirs) != 1 || dirs[0] != expiredResolved {
		t.Fatalf("fencedDirs() = %v, want [%s]", dirs, expiredResolved)
	}

	if _, ok := lookupRegistered(expired); ok {
		t.Fatal("lookupRegistered() must report false for an expired entry")
	}
}

func TestAllowedDirsEmptyOnCorruptRegistry(t *testing.T) {
	t.Setenv("SOUNDINGS_CACHE_DIR", t.TempDir())
	entries, err := entriesDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(entries, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entries, "corrupt.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if dirs := allowedDirs(); len(dirs) != 0 {
		t.Fatalf("allowedDirs() on corrupt registry = %v, want empty", dirs)
	}
}

func TestUnderDir(t *testing.T) {
	cases := []struct {
		path, dir string
		fold      bool
		want      bool
	}{
		{"/tmp/soundings-a", "/tmp/soundings-a", false, true},
		{"/tmp/soundings-a/index.json", "/tmp/soundings-a", false, true},
		{"/tmp/soundings-a/patches/x/y.patch", "/tmp/soundings-a", false, true},
		{"/tmp/soundings-a-evil", "/tmp/soundings-a", false, false},
		{"/tmp/soundings-a-evil/f", "/tmp/soundings-a", false, false},
		{"/tmp", "/tmp/soundings-a", false, false},
		{"/etc/passwd", "/tmp/soundings-a", false, false},
		// Folded (deny direction): a case-variant path names the same
		// directory on the macOS default filesystem and must stay fenced.
		{"/PRIVATE/tmp/soundings-a/index.json", "/private/tmp/soundings-a", true, true},
		{"/private/TMP/Soundings-A", "/private/tmp/soundings-a", true, true},
		// Strict (allow direction): a case variant may be a genuinely
		// distinct directory on a case-sensitive filesystem.
		{"/PRIVATE/tmp/soundings-a/index.json", "/private/tmp/soundings-a", false, false},
	}
	for _, c := range cases {
		if got := underDir(c.path, c.dir, c.fold); got != c.want {
			t.Errorf("underDir(%q, %q, %v) = %v, want %v", c.path, c.dir, c.fold, got, c.want)
		}
	}
}
