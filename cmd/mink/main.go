package main

import "os"

func main() {
	if len(os.Args) < 2 {
		runCLI()
		return
	}

	switch os.Args[1] {
	case "version":
		runVersion()
	case "web":
		runWeb()
	case "tg":
		runTG()
	case "mcp-bridge":
		runMCPBridge()
	default:
		os.Args = append([]string{os.Args[0]}, os.Args[1:]...)
		runCLI()
	}
}
