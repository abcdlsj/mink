package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/computerhost"
	"github.com/abcdlsj/sumi/internal/computerstate"
	"github.com/abcdlsj/sumi/internal/home"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	defaultRoot, err := home.DefaultRoot()
	if err != nil {
		return err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("resolve computer name: %w", err)
	}
	flags := flag.NewFlagSet("sumi-computer", flag.ContinueOnError)
	serverURL := flags.String("server", "http://127.0.0.1:8080", "Sumi Server URL")
	dataRoot := flags.String("data-root", defaultRoot, "Computer data root")
	keyFile := flags.String("registration-key-file", filepath.Join(defaultRoot, "computer.key"), "0600 registration key file")
	name := flags.String("name", hostname, "Computer display name")
	once := flags.Bool("once", false, "Register and synchronize pending assignments once")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*once {
		return errors.New("continuous mode is not available; use --once")
	}
	state, err := computerstate.Open(*dataRoot)
	if err != nil {
		return err
	}
	defer state.Close()
	identity, found, err := state.Identity(context.Background())
	if err != nil {
		return err
	}
	key := ""
	if found {
		key = identity.RegistrationKey
	} else {
		key, err = computerhost.ReadRegistrationKey(*keyFile)
		if err != nil {
			return err
		}
	}
	osName, arch, err := platform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	host := computerhost.New(computerhost.Config{
		ServerURL:       *serverURL,
		DataRoot:        *dataRoot,
		RegistrationKey: key,
		Name:            *name,
		OS:              osName,
		Arch:            arch,
		State:           state,
	})
	result, err := host.SyncOnce(context.Background())
	if err != nil {
		return err
	}
	log.Printf("Computer %s synchronized %d assignments", result.ComputerID, result.Assignments)
	return nil
}

func platform(goos, goarch string) (computerv1.OperatingSystem, computerv1.Architecture, error) {
	var osName computerv1.OperatingSystem
	switch goos {
	case "darwin":
		osName = computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS
	case "linux":
		osName = computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX
	default:
		return 0, 0, fmt.Errorf("unsupported operating system %q", goos)
	}
	var arch computerv1.Architecture
	switch goarch {
	case "arm64":
		arch = computerv1.Architecture_ARCHITECTURE_ARM64
	case "amd64":
		arch = computerv1.Architecture_ARCHITECTURE_AMD64
	default:
		return 0, 0, fmt.Errorf("unsupported architecture %q", goarch)
	}
	return osName, arch, nil
}
