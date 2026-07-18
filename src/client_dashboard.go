package yarouter

import (
	"context"
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	clientpkg "github.com/duvu/ya-router/internal/client"
	controlpkg "github.com/duvu/ya-router/internal/control"
	"github.com/duvu/ya-router/internal/secret"
)

type dashboardBackend interface {
	Meta(context.Context) (controlpkg.MetaResponse, error)
	Providers(context.Context) ([]controlpkg.ProviderResource, error)
	Accounts(context.Context) ([]controlpkg.AccountResource, error)
	Models(context.Context, bool) (controlpkg.ModelCatalogResponse, error)
	Configuration(context.Context) (controlpkg.ConfigResource, error)
	RoutingStatus(context.Context) (controlpkg.RoutingStatusResource, error)
	Operations(context.Context) ([]controlpkg.OperationResource, error)
	Events(context.Context, uint64) (controlpkg.EventPage, error)
	Secrets(context.Context) ([]secret.Metadata, error)
	ApplyMutation(context.Context, controlpkg.MutationRequest) (clientpkg.MutationResult, error)
	CreateAuthSession(context.Context, clientpkg.AuthSessionRequest) (controlpkg.OperationResource, error)
	CancelAuthSession(context.Context, string) (controlpkg.OperationResource, error)
	SetSecret(context.Context, string, string) (secret.Metadata, error)
}

var clientDashboardRunner = runClientDashboard

func runClientDashboardCommand(args []string) int {
	set := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	var common clientCommonFlags
	registerClientFlags(set, &common, false, false, false)
	if err := set.Parse(args); err != nil {
		return clientExitUsage
	}
	if common.jsonOut {
		fmt.Fprintln(os.Stderr, "ya: dashboard does not support --json")
		return clientExitUsage
	}
	return clientDashboardRunner(common)
}

func runClientDashboard(common clientCommonFlags) int {
	profile, err := resolveClientProfile(common)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ya: dashboard control endpoint is not configured")
		return clientExitUsage
	}
	backend, err := clientpkg.New(profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ya: dashboard could not create a control client")
		return clientExitUsage
	}
	// The WS client (live chat/status, #74-#77) only supports the local
	// Unix-socket transport; a remote HTTPS profile still gets the full
	// dashboard with REST-only status (no chat), matching the epic's
	// remote-WSS non-goal.
	var wsClient *clientpkg.WSClient
	if profile.Transport == clientpkg.TransportUnix {
		wsClient, _ = clientpkg.NewWSClient(profile)
	}
	program := tea.NewProgram(newWorkspaceModel(common, backend, wsClient), tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ya: dashboard could not start")
		return clientExitRuntimeFailure
	}
	return clientExitOK
}
