package releasebundle

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

type BuildConfig struct {
	Version string
	OS      string
	Arch    string
	Binary  string
	WebRoot string
	Output  string
}

func Build(config BuildConfig) error {
	if !releaseVersionPattern.MatchString(config.Version) || config.OS == "" || config.Arch == "" || config.Output == "" {
		return errors.New("release bundle build configuration is invalid")
	}
	webInfo, err := os.Lstat(config.WebRoot)
	if err != nil || webInfo.Mode()&os.ModeSymlink != 0 || !webInfo.IsDir() {
		return errors.New("release Web root is unsafe")
	}
	if err := os.Mkdir(config.Output, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("release bundle output already exists")
		}
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(config.Output)
		}
	}()
	files := make([]File, 0, 16)
	binaryRecord, err := fileRecord(config.Binary, "bin/sumi")
	if err != nil {
		return err
	}
	if err := copyRegular(config.Binary, filepath.Join(config.Output, "bin", "sumi"), true); err != nil {
		return err
	}
	files = append(files, binaryRecord)
	err = filepath.WalkDir(config.WebRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == config.WebRoot {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("release Web root contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(config.WebRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(filepath.Join("web", relative))
		record, err := fileRecord(path, relative)
		if err != nil {
			return err
		}
		if err := copyRegular(path, filepath.Join(config.Output, filepath.FromSlash(relative)), false); err != nil {
			return err
		}
		files = append(files, record)
		return nil
	})
	if err != nil {
		return err
	}
	manifest := Manifest{Version: ManifestVersion, ReleaseVersion: config.Version, OS: config.OS, Arch: config.Arch, Files: sortedFiles(files)}
	if _, err := decodeManifest(mustJSON(manifest)); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if err := writeNew(filepath.Join(config.Output, "manifest.json"), payload, 0o600); err != nil {
		return err
	}
	if err := syncDirectory(config.Output); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func mustJSON(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}
