package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var Version = "dev"

type semver struct {
	Major, Minor, Patch int
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
	Body string `json:"body"`
}

type CheckResult struct {
	CurrentVersion string
	LatestVersion  string
	HasUpdate      bool
	ReleaseNotes   string
	DownloadURL    string
	AssetName      string
	SHA256         string
}

var githubAPI = "https://api.github.com/repos/InferPort/c2go/releases/latest"

var httpClient = &http.Client{Timeout: 15 * time.Second}

var osExecutable = os.Executable
var filepathEvalSymlinks = filepath.EvalSymlinks
var osRename = os.Rename
var osRemove = os.Remove
var osChmod = os.Chmod

func parseVersion(v string) (semver, error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, fmt.Errorf("invalid semver: %s", v)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return semver{}, fmt.Errorf("invalid major version in %s", v)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return semver{}, fmt.Errorf("invalid minor version in %s", v)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return semver{}, fmt.Errorf("invalid patch version in %s", v)
	}
	return semver{Major: major, Minor: minor, Patch: patch}, nil
}

func (a semver) LessThan(b semver) bool {
	if a.Major != b.Major {
		return a.Major < b.Major
	}
	if a.Minor != b.Minor {
		return a.Minor < b.Minor
	}
	return a.Patch < b.Patch
}

var assetName = func() string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	switch goarch {
	case "amd64":
	case "386":
	case "arm64":
	case "arm":
	default:
		goarch = "amd64"
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("c2go-%s-%s.%s", goos, goarch, ext)
}

func CheckForUpdate(ctx context.Context) (*CheckResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to decode release json: %w", err)
	}

	if release.TagName == "" {
		return nil, fmt.Errorf("no releases found")
	}

	currentVer, err := parseVersion(Version)
	hasVersion := err == nil

	latestVer, err := parseVersion(release.TagName)
	if err != nil {
		return nil, fmt.Errorf("invalid latest version tag: %w", err)
	}

	targetAsset := assetName()
	result := &CheckResult{
		CurrentVersion: Version,
		LatestVersion:  release.TagName,
		HasUpdate:      hasVersion && currentVer.LessThan(latestVer),
		ReleaseNotes:   release.Body,
		AssetName:      targetAsset,
	}

	for _, a := range release.Assets {
		if a.Name == targetAsset {
			result.DownloadURL = a.BrowserDownloadURL
		}
	}

	for _, a := range release.Assets {
		if a.Name == "sha256sums.txt" {
			shaReq, err := http.NewRequestWithContext(ctx, http.MethodGet, a.BrowserDownloadURL, nil)
			if err != nil {
				continue
			}
			shaResp, err := httpClient.Do(shaReq)
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(shaResp.Body)
			shaResp.Body.Close()
			for _, line := range strings.Split(string(body), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasSuffix(line, "  "+targetAsset) || strings.HasSuffix(line, " *"+targetAsset) {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						result.SHA256 = fields[0]
					}
				}
			}
			break
		}
	}

	return result, nil
}

func downloadFile(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func verifySHA256(filePath, expectedHex string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != strings.ToLower(expectedHex) {
		return fmt.Errorf("sha256 mismatch: expected %s, got %s", expectedHex, got)
	}

	return nil
}

func extractTarGz(archivePath, destDir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.FileInfo().IsDir() {
			continue
		}

		destPath := filepath.Join(destDir, filepath.Base(hdr.Name))
		outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, hdr.FileInfo().Mode())
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(outFile, tr); err != nil {
			outFile.Close()
			return "", err
		}
		outFile.Close()
		return destPath, nil
	}

	return "", fmt.Errorf("no binary found in %s", archivePath)
}

func extractZip(archivePath, destDir string) (string, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return "", err
		}

		destPath := filepath.Join(destDir, filepath.Base(f.Name))
		outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return "", err
		}
		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return "", err
		}
		return destPath, nil
	}

	return "", fmt.Errorf("no binary found in %s", archivePath)
}

func (r *CheckResult) DownloadAndVerify(ctx context.Context, destDir string) (string, error) {
	if r.DownloadURL == "" {
		return "", fmt.Errorf("no download url available for %s", r.AssetName)
	}

	archivePath := filepath.Join(destDir, r.AssetName)
	if err := downloadFile(ctx, r.DownloadURL, archivePath); err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	if r.SHA256 != "" {
		if err := verifySHA256(archivePath, r.SHA256); err != nil {
			os.Remove(archivePath)
			return "", fmt.Errorf("sha256 verification failed: %w", err)
		}
	}

	var binaryPath string
	var err error
	if strings.HasSuffix(r.AssetName, ".tar.gz") {
		binaryPath, err = extractTarGz(archivePath, destDir)
	} else if strings.HasSuffix(r.AssetName, ".zip") {
		binaryPath, err = extractZip(archivePath, destDir)
	} else {
		os.Remove(archivePath)
		return "", fmt.Errorf("unsupported archive format: %s", r.AssetName)
	}

	os.Remove(archivePath)
	if err != nil {
		return "", err
	}

	return binaryPath, nil
}

func ApplyUpdate(newBinaryPath string) error {
	exePath, err := osExecutable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	exePath, err = filepathEvalSymlinks(exePath)
	if err != nil {
		return err
	}

	oldPath := exePath + ".old"
	if err := osRename(exePath, oldPath); err != nil {
		return fmt.Errorf("cannot rename current binary: %w", err)
	}

	if err := osRename(newBinaryPath, exePath); err != nil {
		osRename(oldPath, exePath)
		return fmt.Errorf("cannot install new binary: %w", err)
	}

	if runtime.GOOS != "windows" {
		if err := osChmod(exePath, 0755); err != nil {
			return fmt.Errorf("cannot set permissions: %w", err)
		}
	}

	osRemove(oldPath)

	return nil
}

func CleanupOldBinary() {
	exePath, err := osExecutable()
	if err != nil {
		return
	}
	osRemove(exePath + ".old")
}
