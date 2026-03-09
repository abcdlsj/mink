package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	repoOwner = "abcdlsj"
	repoName  = "mink"
)

var ErrAlreadyLatest = errors.New("already at latest version")

type Updater struct {
	currentVersion string
	httpClient     *http.Client
}

func New(currentVersion string) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		httpClient:     &http.Client{},
	}
}

func (u *Updater) Update() error {
	version, downloadURL, err := u.getLatestRelease()
	if err != nil {
		return fmt.Errorf("failed to get latest release: %w", err)
	}
	if version == u.currentVersion {
		return fmt.Errorf("%w: %s", ErrAlreadyLatest, version)
	}

	fmt.Printf("Current version: %s\n", u.currentVersion)
	fmt.Printf("Latest version: %s\n", version)
	fmt.Printf("Downloading from: %s\n", downloadURL)

	tmpFile, err := u.download(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer os.Remove(tmpFile)

	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	currentPath, err = filepath.EvalSymlinks(currentPath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	fmt.Printf("Replacing binary: %s\n", currentPath)
	if err := u.replaceBinary(currentPath, tmpFile); err != nil {
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	fmt.Printf("Successfully updated to %s\n", version)
	return nil
}

func (u *Updater) getLatestRelease() (version, downloadURL string, err error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)

	resp, err := u.httpClient.Get(url)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", err
	}

	version = release.TagName
	for _, candidate := range u.assetNameCandidates() {
		for _, asset := range release.Assets {
			if assetMatches(asset.Name, candidate) {
				return version, asset.URL, nil
			}
		}
	}

	return "", "", fmt.Errorf("no matching asset found for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func (u *Updater) assetNameCandidates() []string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	archAliases := []string{goarch}
	osAliases := []string{goos}

	switch goarch {
	case "amd64":
		archAliases = append(archAliases, "x86_64")
	case "arm64":
		archAliases = append(archAliases, "aarch64")
	}

	switch goos {
	case "darwin":
		osAliases = append(osAliases, "Darwin", "macos", "MacOS")
	case "linux":
		osAliases = append(osAliases, "Linux")
	}

	seen := map[string]struct{}{}
	var candidates []string
	for _, osName := range osAliases {
		for _, archName := range archAliases {
			for _, pattern := range []string{
				fmt.Sprintf("mink-%s-%s", strings.ToLower(osName), strings.ToLower(archName)),
				fmt.Sprintf("mink_%s_%s", osName, archName),
				fmt.Sprintf("%s_%s", osName, archName),
			} {
				if _, ok := seen[pattern]; ok {
					continue
				}
				seen[pattern] = struct{}{}
				candidates = append(candidates, pattern)
			}
		}
	}
	return candidates
}

func assetMatches(name, candidate string) bool {
	name = strings.ToLower(name)
	candidate = strings.ToLower(candidate)
	return name == candidate || strings.Contains(name, candidate)
}

func (u *Updater) download(url string) (string, error) {
	resp, err := u.httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %d", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "mink-update-*")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}
	if err := os.Chmod(tmpFile.Name(), 0o755); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}

func (u *Updater) replaceBinary(currentPath, newPath string) error {
	backupPath := currentPath + ".backup"
	if err := os.Rename(currentPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup current binary: %w", err)
	}
	if err := os.Rename(newPath, currentPath); err != nil {
		os.Rename(backupPath, currentPath)
		return fmt.Errorf("failed to install new binary: %w", err)
	}
	os.Remove(backupPath)
	return nil
}
