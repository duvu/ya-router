// main.go — entry point and command dispatcher.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	switch os.Args[1] {
	case "help", "--help", "-h":
		printUsage()

	case "auth":
		authCmd := flag.NewFlagSet("auth", flag.ExitOnError)
		modeFlag := authCmd.String("mode", "device_code", "Auth mode: device_code (default), api_key (codex only)")
		tokenFlag := authCmd.String("token", "", "Manually set an access token (codex only)")
		args := os.Args[2:]
		provider := "copilot"
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			provider = args[0]
			args = args[1:]
		}
		authCmd.Parse(args) //nolint:errcheck
		var err error
		switch provider {
		case "codex":
			if *tokenFlag != "" {
				err = handleAuthCodexManualToken(*tokenFlag)
			} else {
				if *modeFlag != "device_code" && *modeFlag != "api_key" {
					fmt.Printf("auth codex: invalid --mode %q (use device_code or api_key)\n", *modeFlag)
					os.Exit(1)
				}
				err = handleAuthCodex(*modeFlag)
			}
		default:
			err = handleAuthCopilot(*modeFlag)
		}
		if err != nil {
			fmt.Printf("Authentication failed: %v\n", err)
			os.Exit(1)
		}

	case "run", "start":
		runCmd := flag.NewFlagSet("run", flag.ExitOnError)
		configMigrate := runCmd.String("config-migrate", "merge",
			"Config migration mode: none, merge, override")
		runCmd.Parse(os.Args[2:]) //nolint:errcheck
		mode := ConfigMigrationMode(*configMigrate)
		if mode != ConfigMigrationNone && mode != ConfigMigrationMerge && mode != ConfigMigrationOverride {
			fmt.Printf("Invalid config-migrate mode: %s\n", mode)
			os.Exit(1)
		}
		if err := handleRunWithMigration(mode); err != nil {
			fmt.Printf("Server failed: %v\n", err)
			os.Exit(1)
		}

	case "migrate-config":
		mc := flag.NewFlagSet("migrate-config", flag.ExitOnError)
		modeStr := mc.String("mode", "merge", "Migration mode: merge, override")
		mc.Parse(os.Args[2:]) //nolint:errcheck
		mode := ConfigMigrationMode(*modeStr)
		if mode != ConfigMigrationMerge && mode != ConfigMigrationOverride {
			fmt.Printf("Invalid mode: %s\n", mode)
			os.Exit(1)
		}
		if err := migrateConfig(mode); err != nil {
			fmt.Printf("Config migration failed: %v\n", err)
			os.Exit(1)
		}

	case "models":
		pf := flag.NewFlagSet("models", flag.ExitOnError)
		providerFlag := pf.String("provider", "", "Filter to a specific provider")
		pf.Parse(os.Args[2:]) //nolint:errcheck
		if err := handleModels(*providerFlag); err != nil {
			fmt.Printf("Models failed: %v\n", err)
			os.Exit(1)
		}

	case "config":
		if err := handleConfig(); err != nil {
			fmt.Printf("Config failed: %v\n", err)
			os.Exit(1)
		}

	case "status":
		if err := handleStatus(); err != nil {
			fmt.Printf("Status failed: %v\n", err)
			os.Exit(1)
		}

	case "refresh":
		rf := flag.NewFlagSet("refresh", flag.ExitOnError)
		providerFlag := rf.String("provider", "", "Filter to a specific provider")
		rf.Parse(os.Args[2:]) //nolint:errcheck
		if err := handleRefresh(*providerFlag); err != nil {
			fmt.Printf("Refresh failed: %v\n", err)
			os.Exit(1)
		}

	case "version":
		fmt.Printf("github-copilot-svcs %s\n", version)

	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
