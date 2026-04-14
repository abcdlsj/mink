package main

import (
	"fmt"

	"github.com/abcdlsj/mink"
)

func runVersion() {
	fmt.Printf("mink version %s\n", mink.Version)
	fmt.Printf("  commit: %s\n", mink.Commit)
	fmt.Printf("  built:  %s\n", mink.BuildTime)
}
