package sumi

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	clicontract "github.com/abcdlsj/sumi/internal/cli/contract"
	computercli "github.com/abcdlsj/sumi/internal/computer/cli"
	"github.com/abcdlsj/sumi/internal/home"
	installcore "github.com/abcdlsj/sumi/internal/install"
	"github.com/abcdlsj/sumi/internal/osservice"
)

type serviceController interface {
	Configure(string)
	Start(context.Context, osservice.Component) error
	Stop(context.Context, osservice.Component) error
	Restart(context.Context, osservice.Component) error
	Running(context.Context, osservice.Component) bool
}

var newServiceController = func() (serviceController, error) {
	return osservice.New()
}

var joinComputerPairingCode = computercli.JoinPairingCode

var installedServiceDataRoot = func(fallback string) (string, error) {
	layout, err := installcore.Inspect(fallback)
	if err != nil {
		return "", err
	}
	active, err := installcore.LoadActive(layout)
	if err != nil {
		return "", err
	}
	if active.DataRoot == "" {
		return fallback, nil
	}
	return active.DataRoot, nil
}

var computerInstallReady = func(dataRoot string) error {
	layout, err := installcore.Inspect(dataRoot)
	if err != nil {
		return err
	}
	active, err := installcore.LoadActive(layout)
	if err != nil {
		return err
	}
	if active.DataRoot != "" && active.DataRoot != filepath.Clean(dataRoot) {
		return fmt.Errorf("active install uses a different data root")
	}
	return nil
}

func isServiceAction(action string) bool {
	switch action {
	case "start", "stop", "restart", "status":
		return true
	default:
		return false
	}
}

func runService(ctx context.Context, rawComponent string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || !isServiceAction(args[0]) {
		return invalidCommand(rawComponent, fmt.Sprintf("use 'sumi %s run|start|stop|restart|status'", rawComponent))
	}
	defaultRoot, err := home.DefaultRoot()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("sumi "+rawComponent+" "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataRoot := flags.String("data-root", defaultRoot, "Sumi data root")
	var pairingCode string
	var computerName string
	if rawComponent == string(osservice.Computer) && args[0] == "start" {
		hostname, err := os.Hostname()
		if err != nil {
			return serviceError("Computer name is unavailable", "COMPUTER_NAME_INVALID", "provide --name")
		}
		computerName = hostname
		flags.StringVar(&pairingCode, "pairing-code", "", "one-time Sumi connection code")
		flags.StringVar(&computerName, "name", hostname, "Computer display name")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return serviceError("service arguments are invalid", "INVALID_ARGUMENT", "remove positional arguments")
	}
	nameConfigured := false
	dataRootConfigured := false
	flags.Visit(func(visited *flag.Flag) {
		switch visited.Name {
		case "name":
			nameConfigured = true
		case "data-root":
			dataRootConfigured = true
		}
	})
	if !dataRootConfigured {
		if installedRoot, err := installedServiceDataRoot(*dataRoot); err == nil {
			*dataRoot = installedRoot
		}
	}
	if pairingCode == "" && nameConfigured {
		return serviceError("Computer name requires a pairing code", "INVALID_ARGUMENT", "remove --name or provide --pairing-code")
	}
	component := osservice.Component(rawComponent)
	controller, err := newServiceController()
	if err != nil {
		return serviceError("current-user service manager is unavailable", "SERVICE_UNAVAILABLE", "use foreground run on a supported user session")
	}
	controller.Configure(*dataRoot)
	action := args[0]
	paired := false
	switch action {
	case "start":
		if pairingCode != "" {
			if err := computerInstallReady(*dataRoot); err != nil {
				return serviceError("Sumi is not installed for this user", "INSTALL_REQUIRED", "install Sumi, then retry the same connection command")
			}
			if controller.Running(ctx, component) {
				return serviceError("Computer runtime is active", "RUNTIME_ACTIVE", "stop the running Computer before pairing")
			}
			if err := joinComputerPairingCode(ctx, pairingCode, *dataRoot, computerName); err != nil {
				return err
			}
			paired = true
		}
		err = controller.Start(ctx, component)
	case "stop":
		err = controller.Stop(ctx, component)
	case "restart":
		err = controller.Restart(ctx, component)
	case "status":
		running := controller.Running(ctx, component)
		state := "stopped"
		if running {
			state = "running"
		}
		_, err = fmt.Fprintf(stdout, "%s %s.\n", titleComponent(component), state)
		return err
	}
	if err != nil {
		return serviceError("current-user service command failed", "SERVICE_COMMAND_FAILED", "inspect 'sumi "+rawComponent+" status' and retry")
	}
	if paired {
		_, err = fmt.Fprintln(stdout, "Computer paired and service started.")
		return err
	}
	completed := map[string]string{"start": "started", "stop": "stopped", "restart": "restarted"}[action]
	_, err = fmt.Fprintf(stdout, "%s service %s.\n", titleComponent(component), completed)
	return err
}

func titleComponent(component osservice.Component) string {
	if component == osservice.Server {
		return "Server"
	}
	return "Computer"
}

func serviceError(message, code, next string) error {
	return &clicontract.Error{Message: message, Code: code, NextAction: next}
}
