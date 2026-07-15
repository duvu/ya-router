// main.go — entry point and command dispatcher.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
)

var version = "dev"

func readSecretFromStdin(label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	value, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(value) == "" {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("empty credential")
	}
	return value, nil
}

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
		modeFlag := authCmd.String("mode", "device_code", "Auth mode: device_code (default)")
		apiKeyStdin := authCmd.Bool("api-key-stdin", false, "Read a provider API key from stdin")
		tokenStdin := authCmd.Bool("token-stdin", false, "Read a ChatGPT access token from stdin (fallback only)")
		accountFlag := authCmd.String("account", "", "Account label for the provider account pool")
		args := os.Args[2:]
		provider := "copilot"
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			provider = args[0]
			args = args[1:]
		}
		if err := authCmd.Parse(args); err != nil {
			fmt.Printf("Authentication arguments failed: %v\n", err)
			os.Exit(1)
		}
		if *apiKeyStdin && *tokenStdin {
			fmt.Println("Authentication failed: choose only one of --api-key-stdin or --token-stdin")
			os.Exit(1)
		}

		var err error
		switch provider {
		case "codex":
			switch {
			case *apiKeyStdin:
				var secret string
				secret, err = readSecretFromStdin("OpenAI API key")
				if err == nil {
					err = handleAuthCodexAPIKey(secret, *accountFlag)
				}
			case *tokenStdin:
				var secret string
				secret, err = readSecretFromStdin("ChatGPT access token")
				if err == nil {
					err = handleAuthCodexManualToken(secret, *accountFlag)
				}
			default:
				err = handleAuthCodex(*accountFlag)
			}
		case "copilot":
			err = handleAuthCopilot(*modeFlag, *accountFlag)
		case "kilo":
			if *tokenStdin {
				err = fmt.Errorf("--token-stdin is not supported for Kilo; use --api-key-stdin")
			} else if *apiKeyStdin {
				var secret string
				secret, err = readSecretFromStdin("Kilo API key")
				if err == nil {
					err = handleAuthKiloAPIKey(secret)
				}
			} else {
				err = handleAuthKiloAnonymous()
			}
		default:
			err = fmt.Errorf("unknown provider %q", provider)
		}
		if err != nil {
			fmt.Printf("Authentication failed: %v\n", err)
			os.Exit(1)
		}

	case "run", "start":
		runCmd := flag.NewFlagSet("run", flag.ExitOnError)
		configMigrate := runCmd.String("config-migrate", "merge", "Config migration mode: none, merge, override")
		if err := runCmd.Parse(os.Args[2:]); err != nil {
			fmt.Printf("Run arguments failed: %v\n", err)
			os.Exit(1)
		}
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
		command := flag.NewFlagSet("migrate-config", flag.ExitOnError)
		modeValue := command.String("mode", "merge", "Migration mode: merge, override")
		if err := command.Parse(os.Args[2:]); err != nil {
			fmt.Printf("Migration arguments failed: %v\n", err)
			os.Exit(1)
		}
		mode := ConfigMigrationMode(*modeValue)
		if mode != ConfigMigrationMerge && mode != ConfigMigrationOverride {
			fmt.Printf("Invalid mode: %s\n", mode)
			os.Exit(1)
		}
		if err := migrateConfig(mode); err != nil {
			fmt.Printf("Config migration failed: %v\n", err)
			os.Exit(1)
		}

	case "models":
		command := flag.NewFlagSet("models", flag.ExitOnError)
		providerFlag := command.String("provider", "", "Filter to a specific provider")
		refreshFlag := command.Bool("refresh", false, "Ignore model cache and re-fetch model list")
		if err := command.Parse(os.Args[2:]); err != nil {
			fmt.Printf("Models arguments failed: %v\n", err)
			os.Exit(1)
		}
		if err := handleModels(*providerFlag, *refreshFlag); err != nil {
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
		command := flag.NewFlagSet("refresh", flag.ExitOnError)
		providerFlag := command.String("provider", "", "Filter to a specific provider")
		if err := command.Parse(os.Args[2:]); err != nil {
			fmt.Printf("Refresh arguments failed: %v\n", err)
			os.Exit(1)
		}
		if err := handleRefresh(*providerFlag); err != nil {
			fmt.Printf("Refresh failed: %v\n", err)
			os.Exit(1)
		}

	case "version":
		fmt.Printf("ya-router %s\n", version)

	default:
		fmt.Printf("Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}
