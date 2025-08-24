package main

import (
	"flag"
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

	command := os.Args[1]

	switch command {
	case "help", "--help", "-h":
		printUsage()
		return
	case "auth":
		if err := handleAuth(); err != nil {
			fmt.Printf("Authentication failed: %v\n", err)
			os.Exit(1)
		}
	case "run", "start":
		// Parse run-specific flags
		runCmd := flag.NewFlagSet("run", flag.ExitOnError)
		configMigrate := runCmd.String("config-migrate", "merge", "Config migration mode: none, merge, override")
		runCmd.Parse(os.Args[2:])

		// Validate migration mode
		mode := ConfigMigrationMode(*configMigrate)
		if mode != ConfigMigrationNone && mode != ConfigMigrationMerge && mode != ConfigMigrationOverride {
			fmt.Printf("Invalid config migration mode: %s (valid options: none, merge, override)\n", *configMigrate)
			os.Exit(1)
		}

		if err := handleRunWithMigration(mode); err != nil {
			fmt.Printf("Server failed: %v\n", err)
			os.Exit(1)
		}
	case "migrate-config":
		// Standalone config migration command
		migrateCmd := flag.NewFlagSet("migrate-config", flag.ExitOnError)
		configMode := migrateCmd.String("mode", "merge", "Migration mode: merge, override")
		migrateCmd.Parse(os.Args[2:])

		mode := ConfigMigrationMode(*configMode)
		if mode != ConfigMigrationMerge && mode != ConfigMigrationOverride {
			fmt.Printf("Invalid migration mode: %s (valid options: merge, override)\n", *configMode)
			os.Exit(1)
		}

		if err := migrateConfig(mode); err != nil {
			fmt.Printf("Config migration failed: %v\n", err)
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
