package sumi

import (
	"context"
	"flag"
	"fmt"
	"io"

	clicontract "github.com/abcdlsj/sumi/internal/cli/contract"
	"github.com/abcdlsj/sumi/internal/home"
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
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return serviceError("service arguments are invalid", "INVALID_ARGUMENT", "remove positional arguments")
	}
	component := osservice.Component(rawComponent)
	controller, err := newServiceController()
	if err != nil {
		return serviceError("current-user service manager is unavailable", "SERVICE_UNAVAILABLE", "use foreground run on a supported user session")
	}
	controller.Configure(*dataRoot)
	action := args[0]
	switch action {
	case "start":
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
