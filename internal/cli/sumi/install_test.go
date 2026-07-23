package sumi

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	clicontract "github.com/abcdlsj/sumi/internal/cli/contract"
	installcore "github.com/abcdlsj/sumi/internal/install"
)

type fakeInstaller struct {
	command string
	purge   bool
	err     error
}

func (installer *fakeInstaller) Install(context.Context, string) error {
	installer.command = "install"
	return installer.err
}
func (installer *fakeInstaller) Upgrade(context.Context, string) error {
	installer.command = "upgrade"
	return installer.err
}
func (installer *fakeInstaller) Uninstall(_ context.Context, purge bool) error {
	installer.command = "uninstall"
	installer.purge = purge
	return installer.err
}

func TestInstallUpgradeAndConfirmedPurgeRoutes(t *testing.T) {
	fake := &fakeInstaller{}
	original := newInstaller
	newInstaller = func(string) (installer, error) { return fake, nil }
	t.Cleanup(func() { newInstaller = original })
	for _, args := range [][]string{{"install", "--bundle", "/bundle"}, {"upgrade", "--bundle", "/bundle"}, {"uninstall", "--purge-data", "--yes"}} {
		var stdout bytes.Buffer
		if err := Run(context.Background(), args, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
			t.Fatalf("Run(%v) = %v", args, err)
		}
	}
	if fake.command != "uninstall" || !fake.purge {
		t.Fatalf("installer = %+v", fake)
	}
}

func TestPurgeRequiresExactConfirmationAndRestoreFailureIsStable(t *testing.T) {
	fake := &fakeInstaller{}
	original := newInstaller
	newInstaller = func(string) (installer, error) { return fake, nil }
	t.Cleanup(func() { newInstaller = original })
	err := Run(context.Background(), []string{"uninstall", "--purge-data"}, strings.NewReader("no\n"), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("unconfirmed purge was accepted")
	}
	fake.err = installcore.ErrRestoreUnproven
	err = Run(context.Background(), []string{"upgrade", "--bundle", "/bundle"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	var structured *clicontract.Error
	if err == nil || !errors.As(err, &structured) || structured.Code != "RESTORE_UNPROVEN" {
		t.Fatalf("restore error = %v", err)
	}
}
