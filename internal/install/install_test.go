package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/internal/lifecycle"
	"github.com/abcdlsj/sumi/internal/osservice"
	"github.com/abcdlsj/sumi/internal/releasebundle"
)

type fakeServices struct {
	configured string
	installed  osservice.InstallConfig
	running    map[osservice.Component]bool
	failStart  osservice.Component
}

func newFakeServices() *fakeServices {
	return &fakeServices{running: map[osservice.Component]bool{}}
}

func (services *fakeServices) Configure(root string) { services.configured = root }
func (services *fakeServices) Install(_ context.Context, config osservice.InstallConfig) error {
	services.installed = config
	return nil
}
func (services *fakeServices) Start(_ context.Context, component osservice.Component) error {
	if services.failStart == component {
		return errors.New("injected start failure")
	}
	services.running[component] = true
	return nil
}
func (services *fakeServices) Stop(_ context.Context, component osservice.Component) error {
	services.running[component] = false
	return nil
}
func (services *fakeServices) Restart(ctx context.Context, component osservice.Component) error {
	if err := services.Stop(ctx, component); err != nil {
		return err
	}
	return services.Start(ctx, component)
}
func (services *fakeServices) Running(_ context.Context, component osservice.Component) bool {
	return services.running[component]
}
func (services *fakeServices) Uninstall(context.Context) error { return nil }

type fakeProber struct {
	failVersion   string
	mutateVersion string
	probed        []string
}

func (prober *fakeProber) Probe(_ context.Context, binary, _ string, dataRoot string, _ *lifecycle.Lease) error {
	version := filepath.Base(filepath.Dir(filepath.Dir(binary)))
	prober.probed = append(prober.probed, version)
	if version == prober.mutateVersion {
		_ = os.WriteFile(filepath.Join(dataRoot, "data", "server.db"), []byte("candidate-mutated"), 0o600)
	}
	if version == prober.failVersion {
		_ = os.WriteFile(filepath.Join(dataRoot, "data", "server.db"), []byte("candidate-mutated"), 0o600)
		return errors.New("injected candidate failure")
	}
	return nil
}

func TestInstallCreatesVersionAndActiveManifestWithoutChangingFiveEntries(t *testing.T) {
	manager, services := testManager(t)
	bundle := testBundle(t, "1.0.0")
	if err := manager.Install(context.Background(), bundle); err != nil {
		t.Fatal(err)
	}
	active, err := manager.Active()
	if err != nil || active.Release.ReleaseVersion != "1.0.0" || active.DataRoot != manager.Layout.DataRoot {
		t.Fatalf("active = %+v, %v", active, err)
	}
	if services.installed.Binary != manager.Layout.Binary("1.0.0") || services.installed.WebRoot != manager.Layout.WebRoot("1.0.0") {
		t.Fatalf("service install = %+v", services.installed)
	}
	entries, err := os.ReadDir(manager.Layout.DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if strings.Join(names, ",") != "agents,cache,config.toml,data,logs" {
		t.Fatalf("data root entries = %v", names)
	}
}

func TestLegacyActiveManifestWithoutDataRootStillLoads(t *testing.T) {
	manager, _ := testManager(t)
	if err := manager.Install(context.Background(), testBundle(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(manager.Layout.ActiveManifest)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(payload, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "data_root")
	payload, err = json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(manager.Layout.ActiveManifest, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	active, err := manager.Active()
	if err != nil || active.DataRoot != "" || active.Release.ReleaseVersion != "1.0.0" {
		t.Fatalf("legacy active = %+v, %v", active, err)
	}
}

func TestInstallLockSurvivesInstalledTreeRemoval(t *testing.T) {
	manager, _ := testManager(t)
	lock, err := acquireInstallLock(manager.Layout)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := removeSafeTree(manager.Layout.InstallRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireInstallLock(manager.Layout); err == nil {
		t.Fatal("second install operation entered after installed tree removal")
	}
}

func testManager(t *testing.T) (*Manager, *fakeServices) {
	t.Helper()
	homeDirectory := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(homeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", homeDirectory)
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	layout, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	services := newFakeServices()
	services.Configure(layout.DataRoot)
	manager := &Manager{Layout: layout, Services: services, Prober: &fakeProber{}, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	return manager, services
}

func testBundle(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	binary := filepath.Join(root, "sumi")
	if err := os.WriteFile(binary, []byte("binary-"+version), 0o700); err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(root, "web-source")
	if err := os.Mkdir(web, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte(version), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "bundle")
	if err := releasebundle.Build(releasebundle.BuildConfig{Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH, Binary: binary, WebRoot: web, Output: bundle}); err != nil {
		t.Fatal(err)
	}
	return bundle
}
