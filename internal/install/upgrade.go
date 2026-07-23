package install

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/osservice"
	"github.com/abcdlsj/sumi/internal/releasebundle"
	_ "modernc.org/sqlite"
)

const (
	serviceMaintenanceDrainTimeout  = 5 * time.Second
	serviceMaintenanceDrainInterval = 25 * time.Millisecond
)

var (
	serviceMaintenanceNow   = time.Now
	serviceMaintenanceSleep = sleepWithContext
)

func (manager *Manager) Upgrade(ctx context.Context, bundleRoot string) error {
	lock, err := acquireInstallLock(manager.Layout)
	if err != nil {
		return err
	}
	defer lock.Close()
	oldActive, err := LoadActive(manager.Layout)
	if err != nil {
		return errors.New("load active install before upgrade")
	}
	bundle, err := releasebundle.Open(bundleRoot, manager.GOOS, manager.GOARCH)
	if err != nil {
		return err
	}
	if bundle.Manifest.ReleaseVersion == oldActive.Release.ReleaseVersion {
		return errors.New("upgrade release version is already active")
	}
	if err := manager.stopServices(ctx); err != nil {
		return err
	}
	maintenance, err := manager.acquireMaintenanceAfterServiceStop(ctx)
	if err != nil {
		return err
	}
	if err := manager.hook("after-stop"); err != nil {
		maintenance.Close()
		if startErr := manager.startServices(ctx); startErr != nil {
			return ErrRestoreUnproven
		}
		return err
	}
	point, err := createRestorePoint(manager.Layout, oldActive)
	if err != nil {
		maintenance.Close()
		if startErr := manager.startServices(ctx); startErr != nil {
			return ErrRestoreUnproven
		}
		return err
	}
	if err := manager.hook("after-snapshot"); err != nil {
		return manager.recoverUpgrade(ctx, point, oldActive, "", maintenance, err)
	}
	candidateVersion := bundle.Manifest.ReleaseVersion
	candidateRoot := manager.Layout.VersionRoot(candidateVersion)
	if err := bundle.CopyTo(candidateRoot); err != nil {
		return manager.recoverUpgrade(ctx, point, oldActive, "", maintenance, err)
	}
	if err := manager.hook("after-copy"); err != nil {
		return manager.recoverUpgrade(ctx, point, oldActive, candidateVersion, maintenance, err)
	}
	if err := manager.hook("before-candidate-probe"); err != nil {
		return manager.recoverUpgrade(ctx, point, oldActive, candidateVersion, maintenance, err)
	}
	if err := manager.Prober.Probe(ctx, manager.Layout.Binary(candidateVersion), manager.Layout.WebRoot(candidateVersion), manager.Layout.DataRoot, maintenance); err != nil {
		return manager.recoverUpgrade(ctx, point, oldActive, candidateVersion, maintenance, err)
	}
	if err := manager.hook("after-candidate-probe"); err != nil {
		return manager.recoverUpgrade(ctx, point, oldActive, candidateVersion, maintenance, err)
	}
	if err := manager.hook("before-switch"); err != nil {
		return manager.recoverUpgrade(ctx, point, oldActive, candidateVersion, maintenance, err)
	}
	if err := SaveActive(manager.Layout, bundle.Manifest); err != nil {
		return manager.recoverUpgrade(ctx, point, oldActive, candidateVersion, maintenance, err)
	}
	if err := manager.Services.Install(ctx, manager.serviceConfig(candidateVersion)); err != nil {
		return manager.recoverUpgrade(ctx, point, oldActive, candidateVersion, maintenance, err)
	}
	if err := manager.hook("after-switch"); err != nil {
		return manager.recoverUpgrade(ctx, point, oldActive, candidateVersion, maintenance, err)
	}
	if err := maintenance.Close(); err != nil {
		return ErrRestoreUnproven
	}
	if err := manager.startServices(ctx); err != nil {
		_ = manager.stopServices(ctx)
		recoveryLease, leaseErr := lifecycle.AcquireMaintenance(manager.Layout.DataRoot, manager.Layout.RuntimeRoot)
		if leaseErr != nil {
			return ErrRestoreUnproven
		}
		return manager.recoverUpgrade(ctx, point, oldActive, candidateVersion, recoveryLease, err)
	}
	if err := point.Cleanup(); err != nil {
		return err
	}
	return nil
}

func (manager *Manager) acquireMaintenanceAfterServiceStop(ctx context.Context) (*lifecycle.Lease, error) {
	deadline := serviceMaintenanceNow().Add(serviceMaintenanceDrainTimeout)
	for {
		maintenance, err := lifecycle.AcquireMaintenance(manager.Layout.DataRoot, manager.Layout.RuntimeRoot)
		if err == nil {
			return maintenance, nil
		}
		if !errors.Is(err, lifecycle.ErrRuntimeActive) {
			return nil, err
		}
		remaining := deadline.Sub(serviceMaintenanceNow())
		if remaining <= 0 {
			return nil, fmt.Errorf("wait for stopped services to release runtime lease: %w", lifecycle.ErrRuntimeActive)
		}
		if remaining > serviceMaintenanceDrainInterval {
			remaining = serviceMaintenanceDrainInterval
		}
		if err := serviceMaintenanceSleep(ctx, remaining); err != nil {
			return nil, errors.Join(lifecycle.ErrRuntimeActive, err)
		}
	}
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (manager *Manager) recoverUpgrade(ctx context.Context, point *restorePoint, oldActive ActiveManifest, candidateVersion string, maintenance *lifecycle.Lease, cause error) error {
	fail := func() error {
		if maintenance != nil {
			_ = maintenance.Close()
		}
		_ = manager.stopServices(ctx)
		return ErrRestoreUnproven
	}
	if err := manager.hook("before-restore"); err != nil {
		return fail()
	}
	loaded, err := loadRestorePoint(manager.Layout)
	if err != nil || loaded.receipt.ReleaseVersion != oldActive.Release.ReleaseVersion {
		return fail()
	}
	if err := loaded.Restore(); err != nil {
		return fail()
	}
	if err := manager.hook("after-restore"); err != nil {
		return fail()
	}
	if err := manager.Services.Install(ctx, manager.serviceConfig(oldActive.Release.ReleaseVersion)); err != nil {
		return fail()
	}
	if err := manager.hook("before-old-probe"); err != nil {
		return fail()
	}
	if err := manager.Prober.Probe(ctx, manager.Layout.Binary(oldActive.Release.ReleaseVersion), manager.Layout.WebRoot(oldActive.Release.ReleaseVersion), manager.Layout.DataRoot, maintenance); err != nil {
		return fail()
	}
	if err := maintenance.Close(); err != nil {
		return fail()
	}
	maintenance = nil
	if err := manager.hook("before-old-start"); err != nil {
		return fail()
	}
	if err := manager.startServices(ctx); err != nil {
		return fail()
	}
	if candidateVersion != "" && candidateVersion != oldActive.Release.ReleaseVersion {
		_ = removeSafeTree(manager.Layout.VersionRoot(candidateVersion))
	}
	if err := point.Cleanup(); err != nil {
		return ErrRestoreUnproven
	}
	return cause
}

func (manager *Manager) stopServices(ctx context.Context) error {
	if err := manager.Services.Stop(ctx, osservice.Computer); err != nil {
		return errors.New("stop Computer service")
	}
	if err := manager.Services.Stop(ctx, osservice.Server); err != nil {
		return errors.New("stop Server service")
	}
	return nil
}

func (manager *Manager) startServices(ctx context.Context) error {
	if err := manager.Services.Start(ctx, osservice.Server); err != nil {
		return errors.New("start Server service")
	}
	if err := manager.Services.Start(ctx, osservice.Computer); err != nil {
		_ = manager.Services.Stop(ctx, osservice.Server)
		return errors.New("start Computer service")
	}
	return nil
}

type processProber struct{}

func (processProber) Probe(ctx context.Context, binary, webRoot, dataRoot string, maintenance *lifecycle.Lease) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return errors.New("reserve candidate probe address")
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return err
	}
	command := exec.Command(binary, "server", "run", "--listen", address, "--data-root", dataRoot, "--web-root", webRoot)
	command.Env = replaceEnvironment(os.Environ(), "PATH", "")
	var output limitedBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := lifecycle.PrepareMaintenanceChild(maintenance, command); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return errors.New("start candidate Server probe")
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	probeErr := waitForHealth(ctx, "http://"+address+"/healthz")
	if probeErr == nil {
		probeErr = sqliteIntegrity(filepath.Join(dataRoot, "data", "server.db"))
	}
	if command.Process != nil {
		_ = command.Process.Signal(os.Interrupt)
	}
	select {
	case <-wait:
	case <-time.After(5 * time.Second):
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		<-wait
	}
	return probeErr
}

func waitForHealth(ctx context.Context, url string) error {
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("candidate Server health probe timed out")
		case <-ticker.C:
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				return err
			}
			response, err := client.Do(request)
			if err == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusNoContent {
					return nil
				}
			}
		}
	}
}

func sqliteIntegrity(path string) error {
	database, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return errors.New("open candidate Server database")
	}
	defer database.Close()
	var result string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		return errors.New("candidate Server database integrity probe failed")
	}
	var schemaVersion string
	if err := database.QueryRow("SELECT value FROM system_metadata WHERE key = 'schema_version'").Scan(&schemaVersion); err != nil || schemaVersion != "1" {
		return errors.New("candidate Server schema probe failed")
	}
	return nil
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if len(item) < len(prefix) || item[:len(prefix)] != prefix {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}

type limitedBuffer struct {
	bytes.Buffer
}

func (buffer *limitedBuffer) Write(payload []byte) (int, error) {
	const limit = 64 << 10
	accepted := len(payload)
	if buffer.Len() < limit {
		remaining := limit - buffer.Len()
		if len(payload) > remaining {
			payload = payload[:remaining]
		}
		_, _ = buffer.Buffer.Write(payload)
	}
	return accepted, nil
}
