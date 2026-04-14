package main

import (
	"flag"
	"io"
	"net"
	"os"
	"sync"
)

func runMCPBridge() {
	fs := flag.NewFlagSet("mcp-bridge", flag.ExitOnError)
	sockPath := fs.String("sock", "", "Unix socket path to connect to")
	fs.Parse(os.Args[2:])

	if *sockPath == "" {
		fail("mcp-bridge: --sock is required\n")
	}

	conn, err := net.Dial("unix", *sockPath)
	if err != nil {
		fail("mcp-bridge: connect: %v\n", err)
	}
	defer conn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(conn, os.Stdin)
		if c, ok := conn.(*net.UnixConn); ok {
			c.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		io.Copy(os.Stdout, conn)
	}()

	wg.Wait()
}
