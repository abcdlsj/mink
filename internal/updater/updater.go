// Package updater handles self-update functionality for mink binary.
package updater

import (
	"encoding/json"
)

import (
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

// Updater handles downloading and installing mink updates.
type Updater struct {
	currentVersion string
	httpClient     *http.Client
}

// New creates a new Updater instance.
func New(currentVersion string) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		httpClient:     &http.Client{},
	}
}

// Update performs a full update: download latest release and replace binary.
func (u *Updater) Update() error {
	// Get latest release info
	version, downloadURL, err := u.getLatestRelease()
	if err != nil {
		return fmt.Errorf("failed to get latest release: %w", err)
	}

	// Check if already up to date
	if version == u.currentVersion {
		return fmt.Errorf("already at latest version: %s", version)
	}

	fmt.Printf("Current version: %s\n", u.currentVersion)
	fmt.Printf("Latest version: %s\n", version)
	fmt.Printf("Downloading from: %s\n", downloadURL)

	// Download binary
	tmpFile, err := u.download(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer os.Remove(tmpFile)

	// Get current binary path
	currentPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve symlinks
	currentPath, err = filepath.EvalSymlinks(currentPath)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	fmt.Printf("Replacing binary: %s\n", currentPath)

	// Replace binary
	if err := u.replaceBinary(currentPath, tmpFile); err != nil {
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	fmt.Printf("Successfully updated to %s\n", version)
	return nil
}

// getLatestRelease fetches the latest release info from GitHub.
// Returns version tag and download URL.
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

	// Parse response
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

	// Find matching asset for current OS/arch
	assetName := u.getAssetName()
	for _, asset := range release.Assets {
		if strings.Contains(asset.Name, assetName) || asset.Name == fmt.Sprintf("mink_%s", assetName) {
			return version, asset.URL, nil
		}
	}

	return "", "", fmt.Errorf("no matching asset found for %s", assetName)
}

// getAssetName returns the expected asset name for current platform.
func (u *Updater) getAssetName() string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Map to common release naming conventions
	if goos == "darwin" {
		goos = "Darwin"
		if goarch == "amd64" {
			goarch = "x86_64"
		}
	} else if goos == "linux" {
		goos = "Linux"
	}

	return fmt.Sprintf("%s_%s", goos, goarch)
}

// download fetches the binary from URL and returns temp file path.
func (u *Updater) download(url string) (string, error) {
	resp, err := u.httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %d", resp.StatusCode)
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "mink-update-*")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	// Copy content
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	// Make executable
	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}

// replaceBinary atomically replaces the current binary.
func (u *Updater) replaceBinary(currentPath, newPath string) error {
	// On Unix, we can atomically rename over existing file if it's not running
	// But since mink might be running, we need a different approach:
	
	// 1. Rename current binary to .backup
	backupPath := currentPath + ".backup"
	if err := os.Rename(currentPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup current binary: %w", err)
	}

	// 2. Move new binary to current location
	if err := os.Rename(newPath, currentPath); err != nil {
		// Try to restore backup
		os.Rename(backupPath, currentPath)
		return fmt.Errorf("failed to install new binary: %w", err)
	}

	// 3. Remove backup
	os.Remove(backupPath)

	return nil
}
