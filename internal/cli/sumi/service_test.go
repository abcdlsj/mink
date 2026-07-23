package sumi

import (
	"bytes"
	"context"
	"path/filepath"
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
