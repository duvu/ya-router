package yarouter

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	clientpkg "github.com/duvu/ya-router/internal/client"
	controlpkg "github.com/duvu/ya-router/internal/control"
	providerpkg "github.com/duvu/ya-router/internal/provider"
)

func TestWorkspaceModel_LayoutSwitchesAtTwoColumnThreshold(t *testing.T) {
	m := newWorkspaceModel(clientCommonFlags{}, dashboardTestBackend{}, nil)

	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if got := next.(workspaceModel).layout; got != layoutTwoColumn {
		t.Fatalf("layout at 120x30 = %v, want two-column", got)
	}

	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if got := next.(workspaceModel).layout; got != layoutTabbed {
		t.Fatalf("layout at 80x24 = %v, want tabbed", got)
	}
}

func TestWorkspaceModel_TabKeySwitchesTabsOnlyInTabbedLayout(t *testing.T) {
	m := newWorkspaceModel(clientCommonFlags{}, dashboardTestBackend{}, nil)
	m.layout = layoutTabbed
	m.activeTab = tabChat

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	updated := next.(workspaceModel)
	if updated.activeTab != tabStatus {
		t.Fatalf("active tab = %v, want status", updated.activeTab)
	}

	updated.layout = layoutTwoColumn
	next2, _ := updated.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if next2.(workspaceModel).activeTab != tabStatus {
		t.Fatal("tab key must be a no-op in two-column layout")
	}
}

// TestWorkspaceModel_ComposerFocusInterceptsQJKNavigationKeys proves q/j/k
// type into the composer instead of quitting/navigating once focused, and
// that escape releases focus back to global bindings.
func TestWorkspaceModel_ComposerFocusInterceptsQJKNavigationKeys(t *testing.T) {
	m := newWorkspaceModel(clientCommonFlags{}, dashboardTestBackend{}, nil)
	m.chat.focused = true

	for _, r := range []rune{'q', 'j', 'k'} {
		next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(workspaceModel)
	}
	if m.chat.composer != "qjk" {
		t.Fatalf("composer = %q, want qjk (navigation keys must type normally while focused)", m.chat.composer)
	}

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	released := next.(workspaceModel)
	if released.chat.focused {
		t.Fatal("esc must release composer focus")
	}
	// q now quits at the global level.
	_, cmd := released.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q must quit once focus is released")
	}
}

// TestWorkspaceModel_DashboardPaletteOwnsEKey proves that once the
// dashboard's action palette is open, "e" (enable/disable provider) reaches
// dashboardModel.handlePaletteKey rather than being intercepted by any
// workspace-level chat binding, by asserting the resulting mutation request
// carries the selected provider — a workspace-level interception would
// never call ApplyMutation at all.
func TestWorkspaceModel_DashboardPaletteOwnsEKey(t *testing.T) {
	backend := &dashboardMutationBackend{}
	m := newWorkspaceModel(clientCommonFlags{}, backend, nil)
	m.dashboard.snapshot.config.Revision = 7
	m.dashboard.snapshot.providers = []controlpkg.ProviderResource{{
		Descriptor: providerpkg.Descriptor{ID: "copilot"},
		Enabled:    true,
	}}

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = next.(workspaceModel)
	if m.dashboard.mode != dashboardPaletteMode {
		t.Fatalf("mode = %v, want palette", m.dashboard.mode)
	}

	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = next.(workspaceModel)
	if m.dashboard.mode != dashboardConfirmMode {
		t.Fatalf("mode after e = %v, want confirm (dashboard's own toggle flow)", m.dashboard.mode)
	}
	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(workspaceModel)
	if cmd == nil {
		t.Fatal("confirming the toggle did not submit a mutation")
	}
	cmd()
	if backend.request.Provider != "copilot" || backend.request.ExpectedRevision != 7 {
		t.Fatalf("mutation = %+v, want it to reach the dashboard's provider-toggle action", backend.request)
	}
}

// dashboardAuthBackend records CreateAuthSession calls, letting a test
// distinguish "the dashboard's own 'c' (authenticate) action ran" from "the
// workspace's chat-cancel action silently swallowed the keypress" — both
// leave dashboard.mode unchanged, so the call itself is the only reliable
// signal.
type dashboardAuthBackend struct {
	dashboardTestBackend
	calledProviderID string
}

func (backend *dashboardAuthBackend) CreateAuthSession(_ context.Context, request clientpkg.AuthSessionRequest) (controlpkg.OperationResource, error) {
	backend.calledProviderID = request.ProviderID
	return controlpkg.OperationResource{}, nil
}

// TestWorkspaceModel_DashboardPaletteOwnsCKeyForAuthenticate proves that
// while the dashboard's action palette is open, "c" reaches
// dashboardModel.handlePaletteKey's authenticate action (issue #79 requires
// preserving existing provider actions) rather than being intercepted by
// the workspace's own chat-cancel binding, which also binds "c" outside the
// palette.
func TestWorkspaceModel_DashboardPaletteOwnsCKeyForAuthenticate(t *testing.T) {
	backend := &dashboardAuthBackend{}
	m := newWorkspaceModel(clientCommonFlags{}, backend, nil)
	m.dashboard.snapshot.providers = []controlpkg.ProviderResource{{
		Descriptor: providerpkg.Descriptor{ID: "codex"},
		Enabled:    true,
	}}

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}) // open palette
	m = next.(workspaceModel)
	if m.dashboard.mode != dashboardPaletteMode {
		t.Fatalf("mode = %v, want palette", m.dashboard.mode)
	}

	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd == nil {
		t.Fatal("'c' in the palette must invoke the dashboard's authenticate action, got no command")
	}
	cmd() // runs model.startAuth()'s returned action, which calls the backend
	if backend.calledProviderID != "codex" {
		t.Fatalf("CreateAuthSession provider = %q, want codex (workspace must not intercept 'c' while the palette is open)", backend.calledProviderID)
	}
}

// TestWorkspaceModel_GlobalQuitKeyUnaffectedWhenNotFocused proves q quits
// immediately when the composer does not have focus.
func TestWorkspaceModel_GlobalQuitKeyUnaffectedWhenNotFocused(t *testing.T) {
	m := newWorkspaceModel(clientCommonFlags{}, dashboardTestBackend{}, nil)
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q must quit when composer is not focused")
	}
}

// startTestWorkspaceWS serves a real /control/v1/ws over a Unix socket with
// handler, returning a connected *clientpkg.WSClient for use in a
// workspaceModel.
func startTestWorkspaceWS(t *testing.T, handler controlpkg.WSHandler) *clientpkg.WSClient {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	api := controlpkg.NewAPI(controlpkg.APIOptions{ServiceVersion: "test"})
	controlpkg.RegisterWSRoute(api, handler, nil, "test", func() uint64 { return 1 })
	identity := controlpkg.Identity{Subject: "local:test", Role: controlpkg.RoleAdmin, Source: "test"}
	server := &http.Server{Handler: api.Handler(controlpkg.FixedAuthenticator(identity))}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	client, err := clientpkg.NewWSClient(clientpkg.Profile{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type workspaceEchoChatHandler struct{}

func (workspaceEchoChatHandler) HandleSnapshotRequest(_ context.Context, conn *controlpkg.WSConn) {
	payload, _ := json.Marshal(controlpkg.WSStatePayload{
		Providers: []controlpkg.ProviderStateView{{Provider: "copilot", State: "READY"}},
	})
	conn.Send(controlpkg.WSTypeSnapshot, "", payload)
}
func (workspaceEchoChatHandler) HandleChatStart(_ context.Context, conn *controlpkg.WSConn, requestID string, _ controlpkg.WSChatStartPayload) {
	routePayload, _ := json.Marshal(controlpkg.WSChatRoutePayload{Provider: "copilot", ResolvedModel: "gpt-5-mini"})
	conn.Send(controlpkg.WSTypeChatRoute, requestID, routePayload)
	deltaPayload, _ := json.Marshal(controlpkg.WSChatDeltaPayload{Text: "hi there"})
	conn.Send(controlpkg.WSTypeChatDelta, requestID, deltaPayload)
	conn.Send(controlpkg.WSTypeChatDone, requestID, nil)
}
func (workspaceEchoChatHandler) HandleChatCancel(*controlpkg.WSConn) {}

// workspaceNoActiveTargetHandler simulates a routing failure that occurs
// before chat.route is ever sent (e.g. issue #93's no-active-target case),
// exercising the path where no assistant transcript bubble exists yet.
type workspaceNoActiveTargetHandler struct{}

func (workspaceNoActiveTargetHandler) HandleSnapshotRequest(_ context.Context, conn *controlpkg.WSConn) {
	conn.Send(controlpkg.WSTypeSnapshot, "", json.RawMessage(`{}`))
}
func (workspaceNoActiveTargetHandler) HandleChatStart(_ context.Context, conn *controlpkg.WSConn, requestID string, _ controlpkg.WSChatStartPayload) {
	payload, _ := json.Marshal(controlpkg.WSChatErrorPayload{Category: "model_unavailable", Message: "no active target is available for model \"thiendu\""})
	conn.Send(controlpkg.WSTypeChatError, requestID, payload)
}
func (workspaceNoActiveTargetHandler) HandleChatCancel(*controlpkg.WSConn) {}

// TestWorkspaceModel_NoActiveTargetErrorIsVisibleWithoutAssistantBubble
// proves a chat.error that arrives before any chat.route is still surfaced
// to the user (issue #79's status must reflect actual routing outcomes),
// even though chatModel never created an assistant transcript entry for it.
func TestWorkspaceModel_NoActiveTargetErrorIsVisibleWithoutAssistantBubble(t *testing.T) {
	wsClient := startTestWorkspaceWS(t, workspaceNoActiveTargetHandler{})
	m := newWorkspaceModel(clientCommonFlags{}, dashboardTestBackend{}, wsClient)
	t.Cleanup(wsClient.Close)
	wsClient.Connect(context.Background())

	m = drainWorkspaceWSEvents(t, m, clientpkg.WSEventConnected, 3*time.Second)
	m = drainWorkspaceWSEvents(t, m, clientpkg.WSEventSnapshot, 3*time.Second)

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = next.(workspaceModel)
	for _, r := range []rune("hello") {
		next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(workspaceModel)
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(workspaceModel)

	m = drainWorkspaceWSEvents(t, m, clientpkg.WSEventChatError, 3*time.Second)

	if m.chat.lastError == "" {
		t.Fatal("expected the no-active-target error to be recorded in lastError")
	}
	if len(m.chat.transcript) != 1 {
		t.Fatalf("transcript = %+v, want only the user message (no empty assistant bubble)", m.chat.transcript)
	}
	view := m.View()
	if !strings.Contains(view, "no active target") {
		t.Fatalf("rendered view does not surface the routing failure: %s", view)
	}
}

// drainWorkspaceWSEvents applies every currently-available WSEvent to m by
// running it through Update, matching what the real Bubble Tea loop does via
// waitForWSEvent/wsClientEventMsg. It stops once the events channel has no
// immediately-ready value, using a short grace period to absorb network
// scheduling jitter against the local test server.
func drainWorkspaceWSEvents(t *testing.T, m workspaceModel, want clientpkg.WSEventType, timeout time.Duration) workspaceModel {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case event, ok := <-m.wsClient.Events():
			if !ok {
				t.Fatal("ws client events channel closed unexpectedly")
			}
			next, _ := m.handleWSEvent(event)
			m = next
			if event.Type == want {
				return m
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for workspace to observe event %q", want)
	return m
}

// TestWorkspaceModel_EndToEndChatOverRealWSConnection drives the composer
// (focus -> type -> submit) against a real WS server and applies the
// resulting WSClient events through workspaceModel.Update, proving the
// composer, WSClient wiring, and chat rendering all agree with the actual
// chat.route/delta/done sequence and that the transcript/selected-target
// view reflects it.
func TestWorkspaceModel_EndToEndChatOverRealWSConnection(t *testing.T) {
	wsClient := startTestWorkspaceWS(t, workspaceEchoChatHandler{})
	m := newWorkspaceModel(clientCommonFlags{}, dashboardTestBackend{}, wsClient)
	t.Cleanup(wsClient.Close)
	wsClient.Connect(context.Background())

	m = drainWorkspaceWSEvents(t, m, clientpkg.WSEventConnected, 3*time.Second)
	m = drainWorkspaceWSEvents(t, m, clientpkg.WSEventSnapshot, 3*time.Second)
	if len(m.dashboard.snapshot.wsState.Providers) != 1 || m.dashboard.snapshot.wsState.Providers[0].State != "READY" {
		t.Fatalf("workspace did not absorb initial snapshot: %+v", m.dashboard.snapshot.wsState)
	}

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = next.(workspaceModel)
	if !m.chat.focused {
		t.Fatal("composer did not gain focus")
	}
	for _, r := range []rune("hello") {
		next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(workspaceModel)
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(workspaceModel)

	if len(m.chat.transcript) != 1 || m.chat.transcript[0].Text != "hello" {
		t.Fatalf("user message not recorded before route arrives: %+v", m.chat.transcript)
	}

	m = drainWorkspaceWSEvents(t, m, clientpkg.WSEventChatDone, 3*time.Second)

	if m.chat.selectedProvider != "" {
		t.Fatalf("selection should clear after chat.done, got %q", m.chat.selectedProvider)
	}
	if len(m.chat.transcript) != 2 || m.chat.transcript[1].Role != chatRoleAssistant || m.chat.transcript[1].Text != "hi there" {
		t.Fatalf("assistant transcript = %+v", m.chat.transcript)
	}

	view := m.View()
	if !strings.Contains(view, "hi there") {
		t.Fatalf("rendered view missing assistant reply: %s", view)
	}
}
