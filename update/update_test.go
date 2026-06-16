package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input string
		want  semver
	}{
		{"v0.1.0", semver{0, 1, 0}},
		{"v1.2.3", semver{1, 2, 3}},
		{"0.1.42", semver{0, 1, 42}},
		{"v10.20.30", semver{10, 20, 30}},
	}
	for _, tt := range tests {
		got, err := parseVersion(tt.input)
		if err != nil {
			t.Errorf("parseVersion(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseVersion(%q) = %+v, want %+v", tt.input, got, tt.want)
		}
	}
}

func TestParseVersion_Invalid(t *testing.T) {
	invalid := []string{"", "v1", "v1.2", "a.b.c", "v1.2.3.4"}
	for _, v := range invalid {
		_, err := parseVersion(v)
		if err == nil {
			t.Errorf("parseVersion(%q) should have failed", v)
		}
	}
}

func TestSemverLessThan(t *testing.T) {
	tests := []struct {
		a, b semver
		want bool
	}{
		{semver{0, 1, 0}, semver{0, 1, 1}, true},
		{semver{0, 1, 1}, semver{0, 1, 0}, false},
		{semver{0, 1, 0}, semver{0, 2, 0}, true},
		{semver{1, 0, 0}, semver{0, 99, 99}, false},
		{semver{1, 0, 0}, semver{1, 0, 0}, false},
	}
	for _, tt := range tests {
		got := tt.a.LessThan(tt.b)
		if got != tt.want {
			t.Errorf("%+v.LessThan(%+v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	name := assetName()
	if !strings.HasPrefix(name, "c2go-") {
		t.Errorf("asset name should start with c2go-, got %s", name)
	}
	if !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".zip") {
		t.Errorf("asset name should end with .tar.gz or .zip, got %s", name)
	}
	parts := strings.Split(name, "-")
	if len(parts) < 3 {
		t.Errorf("unexpected asset name format: %s", name)
	}
}

func TestCheckForUpdate_NewerVersion(t *testing.T) {
	origVersion := Version
	defer func() { Version = origVersion }()
	Version = "v0.1.0"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/InferPort/c2go/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ghRelease{
			TagName: "v0.2.0",
			Assets: []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			}{
				{Name: assetName(), BrowserDownloadURL: "https://example.com/c2go-test.tar.gz"},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	origAPI := githubAPI
	githubAPI = srv.URL + "/repos/InferPort/c2go/releases/latest"
	defer func() { githubAPI = origAPI }()

	result, err := CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasUpdate {
		t.Error("expected HasUpdate=true")
	}
	if result.LatestVersion != "v0.2.0" {
		t.Errorf("expected v0.2.0, got %s", result.LatestVersion)
	}
	if result.DownloadURL == "" {
		t.Error("expected non-empty DownloadURL")
	}
}

func TestCheckForUpdate_NoUpdate(t *testing.T) {
	origVersion := Version
	defer func() { Version = origVersion }()
	Version = "v0.2.0"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/InferPort/c2go/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ghRelease{
			TagName: "v0.2.0",
			Assets: []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			}{},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	origAPI := githubAPI
	githubAPI = srv.URL + "/repos/InferPort/c2go/releases/latest"
	defer func() { githubAPI = origAPI }()

	result, err := CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.HasUpdate {
		t.Error("expected HasUpdate=false")
	}
}

func TestCheckForUpdate_DevVersion(t *testing.T) {
	origVersion := Version
	defer func() { Version = origVersion }()
	Version = "dev"

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/InferPort/c2go/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ghRelease{
			TagName: "v0.2.0",
			Assets: []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			}{},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	origAPI := githubAPI
	githubAPI = srv.URL + "/repos/InferPort/c2go/releases/latest"
	defer func() { githubAPI = origAPI }()

	result, err := CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.HasUpdate {
		t.Error("expected HasUpdate=false for dev version")
	}
}

func TestCheckForUpdate_WithSHA256(t *testing.T) {
	origVersion := Version
	defer func() { Version = origVersion }()
	Version = "v0.1.0"

	testAsset := "c2go-test-asset.tar.gz"
	shaContent := fmt.Sprintf("abc123deadbeef  %s\n", testAsset)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sha256sums.txt" {
			w.Write([]byte(shaContent))
			return
		}
		json.NewEncoder(w).Encode(ghRelease{
			TagName: "v0.2.0",
			Assets: []struct {
				Name               string `json:"name"`
				BrowserDownloadURL string `json:"browser_download_url"`
			}{
				{Name: testAsset, BrowserDownloadURL: srv.URL + "/asset.tar.gz"},
				{Name: "sha256sums.txt", BrowserDownloadURL: srv.URL + "/sha256sums.txt"},
			},
		})
	}))
	defer srv.Close()

	origAPI := githubAPI
	githubAPI = srv.URL + "/"
	defer func() { githubAPI = origAPI }()

	origAsset := assetName
	assetName = func() string { return testAsset }
	defer func() { assetName = origAsset }()

	result, err := CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.SHA256 != "abc123deadbeef" {
		t.Errorf("expected sha256 abc123deadbeef, got %s", result.SHA256)
	}
}

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.bin")
	content := []byte("hello world")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	h := sha256.Sum256(content)
	expected := hex.EncodeToString(h[:])

	if err := verifySHA256(path, expected); err != nil {
		t.Errorf("verifySHA256 failed: %v", err)
	}

	if err := verifySHA256(path, "00000000"); err == nil {
		t.Error("expected verifySHA256 to fail with bad hash")
	}
}

func TestExtractTarGz(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.tar.gz")

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gzw := gzip.NewWriter(f)
	tw := tar.NewWriter(gzw)

	hdr := &tar.Header{
		Name: "c2go",
		Size: 4,
		Mode: 0755,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("test")); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gzw.Close()
	f.Close()

	outDir := t.TempDir()
	binPath, err := extractTarGz(archivePath, outDir)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "test" {
		t.Errorf("expected test content, got %s", string(data))
	}
}

func TestDownloadAndVerify(t *testing.T) {
	dir := t.TempDir()

	archivePath := filepath.Join(dir, "c2go-test.tar.gz")
	f, _ := os.Create(archivePath)
	gzw := gzip.NewWriter(f)
	tw := tar.NewWriter(gzw)
	tw.WriteHeader(&tar.Header{Name: "c2go", Size: 4, Mode: 0755})
	tw.Write([]byte("test"))
	tw.Close()
	gzw.Close()
	f.Close()

	archiveData, _ := os.ReadFile(archivePath)
	h := sha256.Sum256(archiveData)
	expectedSHA := hex.EncodeToString(h[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archiveData)
	}))
	defer srv.Close()

	result := &CheckResult{
		DownloadURL: srv.URL,
		AssetName:   "c2go-test.tar.gz",
		SHA256:      expectedSHA,
	}

	destDir := t.TempDir()
	binPath, err := result.DownloadAndVerify(context.Background(), destDir)
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "test" {
		t.Errorf("expected test content, got %s", string(data))
	}
}

func TestDownloadAndVerify_BadSHA(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "c2go-test.tar.gz")
	os.WriteFile(archivePath, []byte("bad data"), 0644)
	archiveData, _ := os.ReadFile(archivePath)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archiveData)
	}))
	defer srv.Close()

	result := &CheckResult{
		DownloadURL: srv.URL,
		AssetName:   "c2go-test.tar.gz",
		SHA256:      "00000000000000000000000000000000",
	}

	destDir := t.TempDir()
	_, err := result.DownloadAndVerify(context.Background(), destDir)
	if err == nil {
		t.Error("expected error for bad SHA256")
	}
}

func TestApplyUpdate(t *testing.T) {
	dir := t.TempDir()

	oldBinary := filepath.Join(dir, "c2go")
	if err := os.WriteFile(oldBinary, []byte("old-content"), 0644); err != nil {
		t.Fatal(err)
	}

	newBinary := filepath.Join(dir, "c2go.new")
	if err := os.WriteFile(newBinary, []byte("new-content"), 0644); err != nil {
		t.Fatal(err)
	}

	origExecutable := osExecutable
	osExecutable = func() (string, error) { return oldBinary, nil }
	defer func() { osExecutable = origExecutable }()

	if err := ApplyUpdate(newBinary); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(oldBinary)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-content" {
		t.Errorf("expected new-content, got %s", string(data))
	}

	if _, err := os.Stat(newBinary); !os.IsNotExist(err) {
		t.Errorf("new binary should have been moved")
	}

	if _, err := os.Stat(oldBinary + ".old"); !os.IsNotExist(err) {
		t.Errorf("old backup should have been removed")
	}
}

func TestCleanupOldBinary(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "c2go")
	oldPath := exePath + ".old"

	os.WriteFile(exePath, []byte("current"), 0644)
	os.WriteFile(oldPath, []byte("stale"), 0644)

	origExecutable := osExecutable
	osExecutable = func() (string, error) { return exePath, nil }
	defer func() { osExecutable = origExecutable }()

	CleanupOldBinary()

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("expected stale old binary to be removed")
	}
}

func TestApplyUpdate_RestoreOnFailure(t *testing.T) {
	dir := t.TempDir()
	oldBinary := filepath.Join(dir, "c2go")
	os.WriteFile(oldBinary, []byte("original"), 0644)

	newBinary := filepath.Join(dir, "nonexistent/new")
	os.MkdirAll(filepath.Dir(newBinary), 0755)
	os.WriteFile(newBinary, []byte("content"), 0644)

	origExecutable := osExecutable
	osExecutable = func() (string, error) { return oldBinary, nil }
	defer func() { osExecutable = origExecutable }()

	origEvalSymlinks := filepathEvalSymlinks
	filepathEvalSymlinks = func(path string) (string, error) { return path, nil }
	defer func() { filepathEvalSymlinks = origEvalSymlinks }()

	// Force failure by removing newBinary after rename
	// Actually, let's make the second rename fail
	origRename := osRename
	renameCount := 0
	osRename = func(old, new string) error {
		renameCount++
		if renameCount == 2 {
			return fmt.Errorf("simulated failure")
		}
		return origRename(old, new)
	}
	defer func() { osRename = origRename }()

	err := ApplyUpdate(newBinary)
	if err == nil {
		t.Fatal("expected error")
	}

	data, _ := os.ReadFile(oldBinary)
	if string(data) != "original" {
		t.Errorf("expected original binary to be restored, got %s", string(data))
	}
}
