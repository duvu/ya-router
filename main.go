package main

import (
	"fmt"
	"os"
)

// version will be set by the build process
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	switch os.Args[1] {
	case "help", "--help", "-h":
		printUsage()
		return
	case "auth":
		if err := handleAuth(); err != nil {
			fmt.Printf("Authentication failed: %v\n", err)
			os.Exit(1)
		}
	case "run", "start":
		if err := handleRun(); err != nil {
			fmt.Printf("Server failed: %v\n", err)
			os.Exit(1)
		}
	case "models":
		if err := handleModels(); err != nil {
			fmt.Printf("Models command failed: %v\n", err)
			os.Exit(1)
		}
	case "config":
		if err := handleConfig(); err != nil {
			fmt.Printf("Config command failed: %v\n", err)
			os.Exit(1)
		}
	case "status":
		if err := handleStatus(); err != nil {
			fmt.Printf("Status command failed: %v\n", err)
			os.Exit(1)
		}
	case "refresh":
		if err := handleRefresh(); err != nil {
			fmt.Printf("Refresh command failed: %v\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Printf("github-copilot-svcs version %s\n", version)
	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
