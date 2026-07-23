package authority

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var (
	credentialMigrationBeforePublish = func(string, string) {}
	credentialMigrationLink          = unix.Linkat
	credentialMigrationUnlink        = unix.Unlink
)

type CredentialMigration struct {
	CredentialPath string
	legacyPath     string
	legacy         *os.File
	removeLegacy   bool
}

func PrepareCredentialMigration(legacyPath, credentialPath string) (*CredentialMigration, error) {
	legacy, legacyCredential, legacyFound, err := openMigrationCredential(legacyPath)
	if err != nil {
		return nil, err
	}
	closeLegacy := true
	defer func() {
		if closeLegacy && legacy != nil {
			legacy.Close()
		}
	}()
	current, currentCredential, currentFound, err := openMigrationCredential(credentialPath)
	if current != nil {
		current.Close()
	}
	if err != nil {
		return nil, err
	}
	if legacyFound && currentFound && legacyCredential != currentCredential {
		return nil, errors.New("legacy and current Human credentials do not match")
	}
	if legacyFound && !currentFound {
		if err := publishCredentialNoReplace(credentialPath, legacyCredential); err != nil {
			return nil, err
		}
		current, currentCredential, currentFound, err = openMigrationCredential(credentialPath)
		if current != nil {
			current.Close()
		}
		if err != nil || !currentFound || currentCredential != legacyCredential {
			return nil, errors.New("published Human credential could not be verified")
		}
	}
	closeLegacy = false
	return &CredentialMigration{
		CredentialPath: credentialPath,
		legacyPath:     legacyPath,
		legacy:         legacy,
		removeLegacy:   legacyFound,
	}, nil
}

func (migration *CredentialMigration) Finalize() error {
	if migration == nil || !migration.removeLegacy {
		return nil
	}
	if migration.legacy == nil {
		return errors.New("legacy Human credential identity is unavailable")
	}
	openedInfo, err := migration.legacy.Stat()
	if err != nil {
		return errors.New("inspect legacy Human credential")
	}
	pathInfo, err := os.Lstat(migration.legacyPath)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, pathInfo) {
		return errors.New("legacy Human credential changed during migration")
	}
	if err := credentialMigrationUnlink(migration.legacyPath); err != nil {
		return errors.New("remove legacy Human credential")
	}
	if err := syncCredentialDirectory(filepath.Dir(migration.legacyPath)); err != nil {
		return errors.New("sync legacy Human credential directory")
	}
	migration.removeLegacy = false
	return migration.Close()
}

func (migration *CredentialMigration) Close() error {
	if migration == nil || migration.legacy == nil {
		return nil
	}
	err := migration.legacy.Close()
	migration.legacy = nil
	return err
}

func openMigrationCredential(path string) (*os.File, string, bool, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, errors.New("open Human credential")
	}
	file := os.NewFile(uintptr(descriptor), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		file.Close()
		return nil, "", false, errors.New("human credential is unsafe")
	}
	payload, err := io.ReadAll(io.LimitReader(file, 129))
	if err != nil || len(payload) > 128 || !ValidCredential(string(payload)) {
		file.Close()
		return nil, "", false, errors.New("human credential is invalid")
	}
	return file, string(payload), true, nil
}

func publishCredentialNoReplace(path, credential string) error {
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("human credential directory is unsafe")
	}
	temporary, err := os.CreateTemp(parent, ".human-credential-*.tmp")
	if err != nil {
		return errors.New("create temporary Human credential")
	}
	temporaryPath := temporary.Name()
	defer func() {
		temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return errors.New("secure temporary Human credential")
	}
	if _, err := temporary.WriteString(credential); err != nil {
		return errors.New("write temporary Human credential")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sync temporary Human credential")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close temporary Human credential")
	}
	credentialMigrationBeforePublish(path, credential)
	err = credentialMigrationLink(unix.AT_FDCWD, temporaryPath, unix.AT_FDCWD, path, 0)
	if errors.Is(err, unix.EEXIST) {
		current, currentCredential, found, readErr := openMigrationCredential(path)
		if current != nil {
			current.Close()
		}
		if readErr != nil || !found || currentCredential != credential {
			return errors.New("current Human credential conflicts with legacy credential")
		}
		return nil
	}
	if err != nil {
		return errors.New("publish Human credential")
	}
	if err := syncCredentialDirectory(parent); err != nil {
		return errors.New("sync Human credential directory")
	}
	if err := os.Remove(temporaryPath); err != nil {
		return errors.New("remove temporary Human credential")
	}
	if err := syncCredentialDirectory(parent); err != nil {
		return errors.New("sync Human credential directory")
	}
	return nil
}

func syncCredentialDirectory(path string) error {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(descriptor), path)
	defer directory.Close()
	return directory.Sync()
}
