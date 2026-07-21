package main

import (
	"os"

	"github.com/abcdlsj/sumi/internal/hostcli"
)

func main() {
	if err := hostcli.Run(os.Args[1:], os.Stdout); err != nil {
		hostcli.FormatError(os.Stderr, err)
		os.Exit(1)
	}
}
