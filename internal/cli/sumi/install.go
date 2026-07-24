package sumi

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	clicontract "github.com/abcdlsj/sumi/internal/cli/contract"
	installcore "github.com/abcdlsj/sumi/internal/install"
	"github.com/abcdlsj/sumi/internal/observability"
)

type installer interface {
	Install(context.Context, string) error
	Upgrade(context.Context, string) error
	Uninstall(context.Context, bool) error
}

var newInstaller = func(dataRoot string) (installer, error) {
	return installcore.New(dataRoot)
}

func runInstallCommand(ctx context.Context, command string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sumi "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataRoot := flags.String("data-root", "", "Sumi data root")
	bundle := flags.String("bundle", "", "verified release bundle directory")
	purge := flags.Bool("purge-data", false, "remove all five Sumi data categories")
	yes := flags.Bool("yes", false, "confirm destructive data purge")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return installError("install arguments are invalid", "INVALID_ARGUMENT", "remove positional arguments")
	}
	if command == "uninstall" {
		if *bundle != "" || *yes && !*purge {
			return installError("uninstall arguments are invalid", "INVALID_ARGUMENT", "use --purge-data with optional --yes")
		}
	} else if *bundle == "" || *purge || *yes {
		return installError("release bundle is required", "INVALID_ARGUMENT", "provide --bundle <directory>")
	}
	manager, err := newInstaller(*dataRoot)
	if err != nil {
		return installError("install environment is unsafe", "INSTALL_UNSAFE", "use a supported non-root current-user session")
	}
	if configurable, ok := manager.(interface{ SetLogger(*observability.Logger) }); ok {
		configurable.SetLogger(observability.New(observability.ComponentInstaller, stderr))
	}
	if command == "uninstall" && *purge {
		if err := confirmPurge(stdin, stdout, *yes); err != nil {
			return err
		}
	}
	switch command {
	case "install":
		err = manager.Install(ctx, *bundle)
	case "upgrade":
		err = manager.Upgrade(ctx, *bundle)
	case "uninstall":
		err = manager.Uninstall(ctx, *purge)
	}
	if err != nil {
		if errors.Is(err, installcore.ErrRestoreUnproven) {
			return installError("upgrade restore could not be proven; services remain stopped", "RESTORE_UNPROVEN", "preserve restore evidence and perform an authorized manual recovery")
		}
		return installError("install operation failed", "INSTALL_FAILED", "inspect stable service and doctor status, then retry")
	}
	message := map[string]string{"install": "Sumi installed.", "upgrade": "Sumi upgraded.", "uninstall": "Sumi uninstalled."}[command]
	_, err = fmt.Fprintln(stdout, message)
	return err
}

func confirmPurge(stdin io.Reader, stdout io.Writer, yes bool) error {
	if _, err := fmt.Fprintln(stdout, "Purge will remove:"); err != nil {
		return err
	}
	for _, category := range installcore.PurgeCategories() {
		if _, err := fmt.Fprintln(stdout, "- "+category); err != nil {
			return err
		}
	}
	if yes {
		return nil
	}
	if _, err := fmt.Fprint(stdout, "Type PURGE to continue: "); err != nil {
		return err
	}
	answer, err := bufio.NewReader(io.LimitReader(stdin, 32)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return installError("purge confirmation failed", "PURGE_NOT_CONFIRMED", "retry and type PURGE, or use --yes")
	}
	if strings.TrimSpace(answer) != "PURGE" {
		return installError("purge was not confirmed", "PURGE_NOT_CONFIRMED", "retry and type PURGE, or use --yes")
	}
	return nil
}

func installError(message, code, next string) error {
	return &clicontract.Error{Message: message, Code: code, NextAction: next}
}
