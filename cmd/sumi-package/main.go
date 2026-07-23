package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/abcdlsj/sumi/internal/releasebundle"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "Error: release bundle build failed")
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sumi-package", flag.ContinueOnError)
	flags.SetOutput(stderr)
	version := flags.String("version", "", "release version")
	goos := flags.String("os", runtime.GOOS, "target operating system")
	arch := flags.String("arch", runtime.GOARCH, "target architecture")
	binary := flags.String("binary", "", "unified sumi binary")
	webRoot := flags.String("web", "", "production Web root")
	output := flags.String("out", "", "new release bundle directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *version == "" || *binary == "" || *webRoot == "" || *output == "" {
		return fmt.Errorf("version, binary, web, and out are required")
	}
	if err := releasebundle.Build(releasebundle.BuildConfig{Version: *version, OS: *goos, Arch: *arch, Binary: *binary, WebRoot: *webRoot, Output: *output}); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "Release bundle ready.")
	return err
}
