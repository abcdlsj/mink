package sumi

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/internal/osservice"
)

type fakeServiceController struct {
	root    string
	running bool
}

func (service *fakeServiceController) Configure(root string) { service.root = root }
func (service *fakeServiceController) Start(context.Context, osservice.Component) error {
	service.running = true
	return nil
}
func (service *fakeServiceController) Stop(context.Context, osservice.Component) error {
	service.running = false
	return nil
}
func (service *fakeServiceController) Restart(context.Context, osservice.Component) error {
	service.running = true
	return nil
}
func (service *fakeServiceController) Running(context.Context, osservice.Component) bool {
	return service.running
}

func TestServiceRoutesAreCurrentUserCommands(t *testing.T) {
	service := &fakeServiceController{}
	original := newServiceController
	newServiceController = func() (serviceController, error) { return service, nil }
	t.Cleanup(func() { newServiceController = original })
	root := filepath.Join(t.TempDir(), ".sumi")
	for _, args := range [][]string{{"server", "start"}, {"server", "status"}, {"computer", "restart"}, {"computer", "stop"}} {
		var stdout bytes.Buffer
		if err := Run(context.Background(), append(args, "--data-root", root), bytes.NewReader(nil), &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("Run(%v) = %v", args, err)
		}
	}
	if service.root != root {
		t.Fatalf("configured root = %q", service.root)
	}
}

func TestServiceUsesInstalledDataRootWhenFlagIsOmitted(t *testing.T) {
	service := &fakeServiceController{}
	originalController := newServiceController
	newServiceController = func() (serviceController, error) { return service, nil }
	t.Cleanup(func() { newServiceController = originalController })

	originalInstalledRoot := installedServiceDataRoot
	installedRoot := filepath.Join(t.TempDir(), "installed-sumi")
	installedServiceDataRoot = func(string) (string, error) { return installedRoot, nil }
	t.Cleanup(func() { installedServiceDataRoot = originalInstalledRoot })

	if err := Run(context.Background(), []string{"computer", "status"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if service.root != installedRoot {
		t.Fatalf("configured root = %q, want %q", service.root, installedRoot)
	}
}

func TestComputerStartPairsFromCodeBeforeStartingService(t *testing.T) {
	service := &fakeServiceController{}
	originalController := newServiceController
	newServiceController = func() (serviceController, error) { return service, nil }
	t.Cleanup(func() { newServiceController = originalController })

	originalJoin := joinComputerPairingCode
	originalInstallReady := computerInstallReady
	computerInstallReady = func(string) error { return nil }
	t.Cleanup(func() { computerInstallReady = originalInstallReady })
	var joinedCode, joinedRoot, joinedName string
	joinComputerPairingCode = func(_ context.Context, code, root, name string) error {
		joinedCode, joinedRoot, joinedName = code, root, name
		return nil
	}
	t.Cleanup(func() { joinComputerPairingCode = originalJoin })

	root := filepath.Join(t.TempDir(), ".sumi")
	var stdout bytes.Buffer
	err := Run(context.Background(), []string{
		"computer", "start", "--data-root", root, "--name", "Desk Mac", "--pairing-code", "sumi-pair-v1.secret-sentinel",
	}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if joinedCode != "sumi-pair-v1.secret-sentinel" || joinedRoot != root || joinedName != "Desk Mac" {
		t.Fatalf("join = %q, %q, %q", joinedCode, joinedRoot, joinedName)
	}
	if !service.running || stdout.String() != "Computer paired and service started.\n" {
		t.Fatalf("service/output = %v / %q", service.running, stdout.String())
	}
}

func TestComputerStartRejectsPairingWhileServiceRuns(t *testing.T) {
	service := &fakeServiceController{running: true}
	originalController := newServiceController
	newServiceController = func() (serviceController, error) { return service, nil }
	t.Cleanup(func() { newServiceController = originalController })

	originalJoin := joinComputerPairingCode
	originalInstallReady := computerInstallReady
	computerInstallReady = func(string) error { return nil }
	t.Cleanup(func() { computerInstallReady = originalInstallReady })
	called := false
	joinComputerPairingCode = func(context.Context, string, string, string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { joinComputerPairingCode = originalJoin })

	err := Run(context.Background(), []string{
		"computer", "start", "--pairing-code", "sumi-pair-v1.secret-sentinel",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || called {
		t.Fatalf("running service pairing = %v, called=%v", err, called)
	}
}

func TestComputerStartRequiresInstallBeforeConsumingCode(t *testing.T) {
	service := &fakeServiceController{}
	originalController := newServiceController
	newServiceController = func() (serviceController, error) { return service, nil }
	t.Cleanup(func() { newServiceController = originalController })

	originalInstallReady := computerInstallReady
	computerInstallReady = func(string) error { return errors.New("missing active install") }
	t.Cleanup(func() { computerInstallReady = originalInstallReady })
	originalJoin := joinComputerPairingCode
	called := false
	joinComputerPairingCode = func(context.Context, string, string, string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { joinComputerPairingCode = originalJoin })

	err := Run(context.Background(), []string{
		"computer", "start", "--pairing-code", "sumi-pair-v1.secret-sentinel",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || called || service.running {
		t.Fatalf("uninstalled pairing = %v, called=%v, running=%v", err, called, service.running)
	}
}
