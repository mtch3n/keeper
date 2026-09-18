package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tarGz builds a release-shaped archive: one directory holding the binary,
// exactly as the release workflow lays it out.
func tarGz(t *testing.T, dir, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: dir + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: dir + "/" + name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestFetchChecksumsReadsVersionFromAssetNames covers the thing the command
// relies on rather than a separate API call: the version and the digests come
// out of one file, so the number can never name bytes it did not check.
func TestFetchChecksumsReadsVersionFromAssetNames(t *testing.T) {
	const body = "" +
		"aaa  ./keeper_0.0.9_linux_amd64.tar.gz\n" +
		"bbb  ./keeper_0.0.9_darwin_arm64.tar.gz\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/SHA256SUMS" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()
	t.Setenv("KEEPER_RELEASE_BASE", srv.URL)

	sums, version, err := fetchChecksums(t.Context())
	if err != nil {
		t.Fatalf("fetchChecksums: %v", err)
	}
	if version != "0.0.9" {
		t.Errorf("version = %q, want 0.0.9", version)
	}
	if got := sums["keeper_0.0.9_linux_amd64.tar.gz"]; got != "aaa" {
		t.Errorf("digest = %q, want aaa", got)
	}
	if len(sums) != 2 {
		t.Errorf("sums has %d entries, want 2", len(sums))
	}
}

// An empty or still-uploading release must not read as "no update", which
// would be a silent no-op every time it happened.
func TestFetchChecksumsRejectsAnEmptyRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("\n"))
	}))
	defer srv.Close()
	t.Setenv("KEEPER_RELEASE_BASE", srv.URL)

	if _, _, err := fetchChecksums(t.Context()); err == nil {
		t.Fatal("fetchChecksums accepted a release naming no assets")
	}
}

func TestBinaryFromArchive(t *testing.T) {
	name := "keeper"
	if runtime.GOOS == "windows" {
		name = "keeper.exe"
	}
	want := []byte("\x7fELF not really")
	blob := tarGz(t, "keeper_0.0.9_"+runtime.GOOS+"_"+runtime.GOARCH, name, want)

	got, err := binaryFromArchive(blob)
	if err != nil {
		t.Fatalf("binaryFromArchive: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted %q, want %q", got, want)
	}
}

func TestBinaryFromArchiveRejectsAnArchiveWithoutKeeper(t *testing.T) {
	blob := tarGz(t, "keeper_0.0.9", "README.md", []byte("nothing here"))
	if _, err := binaryFromArchive(blob); err == nil {
		t.Fatal("binaryFromArchive accepted an archive with no keeper in it")
	}
}

// The digest is compared against the archive as downloaded, so a mismatch has
// to be detectable from the bytes alone.
func TestChecksumMismatchIsDetectable(t *testing.T) {
	blob := tarGz(t, "keeper_0.0.9", "keeper", []byte("one"))
	other := sha256.Sum256([]byte("two"))
	if hex.EncodeToString(sha256Sum(blob)) == hex.EncodeToString(other[:]) {
		t.Fatal("distinct payloads hashed the same")
	}
}

func TestReplaceBinaryLeavesTheOldOneOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keeper")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(path, []byte("new")); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want new", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want an executable bit", info.Mode().Perm())
	}
	// Nothing staged is left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".keeper-update-") {
			t.Errorf("staged file %s was left behind", e.Name())
		}
	}
}

// R4.1's reasoning applied to the binary: writing into a package manager's
// tree works as root and is still wrong, because the next upgrade of that
// package silently undoes it.
func TestReplaceableRefusesPackageManagerPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the prefixes are Unix paths")
	}
	for _, path := range []string{
		"/usr/bin/keeper",
		"/usr/sbin/keeper",
		"/nix/store/abc/bin/keeper",
		"/snap/keeper/x/keeper",
		"/opt/homebrew/bin/keeper",
		"/usr/local/Cellar/keeper/0.0.4/bin/keeper",
	} {
		if err := replaceable(path); err == nil {
			t.Errorf("replaceable(%q) allowed a package-managed path", path)
		}
	}
}

// /usr/local/bin is the README's second install location, not package-manager
// territory. Refusing it would decline to update an install done exactly as
// the project documents; what stops an unprivileged run there is the probe.
func TestReplaceableDoesNotTreatUsrLocalBinAsPackageManaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the prefixes are Unix paths")
	}
	err := replaceable("/usr/local/bin/keeper")
	if err != nil && !strings.Contains(err.Error(), "cannot write next to") {
		t.Errorf("replaceable refused /usr/local/bin for the wrong reason: %v", err)
	}
}

func TestReplaceableAllowsAWritableUserPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keeper")
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceable(path); err != nil {
		t.Errorf("replaceable(%q) = %v, want nil", path, err)
	}
}
