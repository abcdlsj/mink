package main

import (
	"fmt"
	"os"

	"github.com/abcdlsj/mink"
	"github.com/abcdlsj/mink/config"
)

func newApp(cfg config.Config) *mink.App {
	app, err := mink.New(mink.Options{Config: cfg})
	if err != nil {
		if err == mink.ErrAPIKeyRequired {
			fmt.Fprintln(os.Stderr, "need api key")
			os.Exit(1)
		}
		fail("error: %v\n", err)
	}
	return app
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(1)
}
