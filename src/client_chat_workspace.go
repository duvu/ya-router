// client_chat_workspace.go turns the existing "ya" dashboard into one chat
// and status workspace (issue #79). It composes chatModel (transcript/
// composer) with the existing dashboardModel-derived status data, adding a
// WSClient for live chat/state delivery alongside the dashboard's existing
// REST polling for provider actions, auth, and secrets.
package yarouter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	clientpkg "github.com/duvu/ya-router/internal/client"
	controlpkg "github.com/duvu/ya-router/internal/control"
)

// workspaceLayout selects the two-column vs. tabbed rendering based on
// terminal width.
type workspaceLayout uint8

const (
	layoutTabbed workspaceLayout = iota
	layoutTwoColumn
)

// workspaceTwoColumnMinWidth is the width at which chat and status render
// side by side; narrower terminals use Chat/Status tabs instead.
const workspaceTwoColumnMinWidth = 100

type workspaceTab uint8

const (
	tabChat workspaceTab = iota
	tabStatus
)

// wsClientEventMsg wraps one WSClient event for delivery through Bubble
// Tea's message loop.
type wsClientEventMsg struct {
	event clientpkg.WSEvent
	ok    bool
}

func waitForWSEvent(events <-chan clientpkg.WSEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		return wsClientEventMsg{event: event, ok: ok}
	}
}

// workspaceModel is the top-level Bubble Tea model for `ya` once #79 wires
// the WS transport in. dashboardModel is embedded so every existing
// provider-action/auth/secret keybinding and its REST polling keeps working
// unchanged; this model layers chat and a compact status view on top.
type workspaceModel struct {
	dashboard dashboardModel
	chat      chatModel

	wsClient      *clientpkg.WSClient
	wsConnected   bool
	requestIDSeq  int
	activeTab     workspaceTab
	layout        workspaceLayout
	statusMessage string
}

func newWorkspaceModel(common clientCommonFlags, backend dashboardBackend, wsClient *clientpkg.WSClient) workspaceModel {
	return workspaceModel{
		dashboard: newDashboardModel(common, backend),
		chat:      newChatModel(),
		wsClient:  wsClient,
	}
}

func (m workspaceModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.dashboard.Init()}
	if m.wsClient != nil {
		m.wsClient.Connect(context.Background())
		cmds = append(cmds, waitForWSEvent(m.wsClient.Events()))
	}
	return tea.Batch(cmds...)
}

func (m workspaceModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.layout = layoutForWidth(msg.Width)
		next, cmd := m.dashboard.Update(msg)
		m.dashboard = next.(dashboardModel)
		return m, cmd
	case wsClientEventMsg:
		if !msg.ok {
			return m, nil // client closed; no more events
		}
		next, cmd := m.handleWSEvent(msg.event)
		return next, tea.Batch(cmd, waitForWSEvent(m.wsClient.Events()))
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	next, cmd := m.dashboard.Update(message)
	m.dashboard = next.(dashboardModel)
	return m, cmd
}

func layoutForWidth(width int) workspaceLayout {
	if width >= workspaceTwoColumnMinWidth {
		return layoutTwoColumn
	}
	return layoutTabbed
}

func (m workspaceModel) handleWSEvent(event clientpkg.WSEvent) (workspaceModel, tea.Cmd) {
	switch event.Type {
	case clientpkg.WSEventConnected:
		m.wsConnected = true
		m.statusMessage = "Live connection established."
	case clientpkg.WSEventDisconnected:
		m.wsConnected = false
		m.statusMessage = "Live connection lost; reconnecting..."
	case clientpkg.WSEventNonRetryable:
		m.wsConnected = false
		m.statusMessage = "Live connection incompatible with this daemon version."
	case clientpkg.WSEventSnapshot, clientpkg.WSEventStateUpdated:
		if event.State != nil {
			m.dashboard.snapshot.wsState = *event.State
		}
	case clientpkg.WSEventChatRoute:
		if event.Route != nil {
			m.chat.onRoute(*event.Route)
		}
	case clientpkg.WSEventChatDelta:
		if event.Delta != nil {
			m.chat.onDelta(*event.Delta)
		}
	case clientpkg.WSEventChatDone:
		m.chat.onDone()
	case clientpkg.WSEventChatError:
		interrupted := event.ChatErr != nil && event.ChatErr.Category == "interrupted"
		m.chat.onError(interrupted, chatErrorMessage(event.ChatErr))
	}
	return m, nil
}

// handleKey routes a keypress to the composer when it has focus (so q/j/k
// type normally instead of navigating), otherwise to global workspace
// bindings. ctrl+c is the one binding that always quits regardless of focus.
func (m workspaceModel) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "ctrl+c" {
		if m.wsClient != nil {
			m.wsClient.Close()
		}
		return m, tea.Quit
	}
	if m.chat.focused {
		return m.handleComposerFocusedKey(key)
	}
	// The dashboard's non-main modes (action palette, confirm, secret entry)
	// own every key while active, matching the existing dashboard's own
	// precedence (see dashboardModel.handleKey) — chat bindings below must
	// not shadow "c" (authenticate)/"n" inside the palette, for example.
	if m.dashboard.mode != dashboardMainMode {
		next, cmd := m.dashboard.Update(key)
		m.dashboard = next.(dashboardModel)
		return m, cmd
	}
	switch key.String() {
	case "q":
		if m.wsClient != nil {
			m.wsClient.Close()
		}
		return m, tea.Quit
	case "tab":
		if m.layout == layoutTabbed {
			m.activeTab = otherTab(m.activeTab)
			return m, nil
		}
	case "i":
		m.chat.focused = true
		return m, nil
	case "c":
		if m.chat.canCancel() && m.wsClient != nil {
			m.wsClient.CancelChat()
		}
		return m, nil
	case "n":
		m.chat = newChatModel()
		return m, nil
	}
	next, cmd := m.dashboard.Update(key)
	m.dashboard = next.(dashboardModel)
	return m, cmd
}

// handleComposerFocusedKey is reached only while the composer has focus.
// Global navigation/quit keys (q, j, k, tab) are intentionally not
// special-cased here — see chatModel.handleComposerKey — so they type into
// the composer instead of triggering navigation. An explicit escape key
// releases focus back to global bindings.
func (m workspaceModel) handleComposerFocusedKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		m.chat.focused = false
		return m, nil
	case tea.KeyEnter:
		if !key.Alt && m.chat.canSend() {
			return m.submitChat()
		}
	}
	m.chat = m.chat.handleComposerKey(key)
	return m, nil
}

func (m workspaceModel) submitChat() (tea.Model, tea.Cmd) {
	text := m.chat.beginUserMessage()
	requestID := nextChatRequestID(&m.requestIDSeq)
	m.chat.activeRequestID = requestID
	if m.wsClient == nil || !m.wsClient.StartChat(requestID, controlpkg.WSChatStartPayload{
		Model:    m.chat.requestedModel,
		Messages: mustMarshalWorkspaceMessages(text),
	}) {
		m.chat.onError(false, "not connected to the daemon")
		m.statusMessage = "Chat could not be sent: not connected."
	}
	return m, nil
}

// chatErrorMessage renders a bounded, redacted chat.error payload as one
// display line. It never surfaces raw upstream error strings — only the
// stable category and the daemon-provided bounded message.
func chatErrorMessage(payload *controlpkg.WSChatErrorPayload) string {
	if payload == nil {
		return "chat failed"
	}
	if payload.Message != "" {
		return payload.Message
	}
	return string(payload.Category)
}

func mustMarshalWorkspaceMessages(userText string) []byte {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	encoded, err := json.Marshal([]message{{Role: "user", Content: userText}})
	if err != nil {
		return []byte(`[]`)
	}
	return encoded
}

func otherTab(tab workspaceTab) workspaceTab {
	if tab == tabChat {
		return tabStatus
	}
	return tabChat
}

func (m workspaceModel) View() string {
	if m.dashboard.width > 0 && (m.dashboard.width < 60 || m.dashboard.height < 16) {
		return "ya workspace\n\nTerminal is too small. Resize to at least 60x16.\n\nq quit\n"
	}
	chatView := m.renderChat()
	statusView := m.renderStatus()

	var body string
	switch m.layout {
	case layoutTwoColumn:
		body = joinWorkspaceColumns(chatView, statusView, m.dashboard.width)
	default:
		if m.activeTab == tabChat {
			body = chatView
		} else {
			body = statusView
		}
	}

	var out strings.Builder
	out.WriteString(m.renderHeader())
	out.WriteString(body)
	out.WriteString(m.renderFooter())
	return out.String()
}

func (m workspaceModel) renderHeader() string {
	connection := "offline"
	if m.wsConnected {
		connection = "live"
	}
	line := fmt.Sprintf("ya  [%s]", connection)
	if m.layout == layoutTabbed {
		marker := func(tab workspaceTab, label string) string {
			if m.activeTab == tab {
				return "[" + label + "]"
			}
			return " " + label + " "
		}
		line += "   " + marker(tabChat, "Chat") + marker(tabStatus, "Status")
	}
	if m.statusMessage != "" {
		line += "   " + m.statusMessage
	}
	return line + "\n\n"
}

func (m workspaceModel) renderFooter() string {
	if m.chat.focused {
		return "\ni composer focused — enter send, alt+enter newline, esc release focus, ctrl+c quit\n"
	}
	switch m.layout {
	case layoutTabbed:
		return "\ni focus composer  tab switch view  c cancel chat  n new chat  q quit\n"
	default:
		return "\ni focus composer  c cancel chat  n new chat  q quit\n"
	}
}

func joinWorkspaceColumns(left, right string, totalWidth int) string {
	rightWidth := totalWidth / 3
	if rightWidth < 24 {
		rightWidth = 24
	}
	leftWidth := totalWidth - rightWidth - 3
	return padColumnsSideBySide(left, right, leftWidth, rightWidth)
}
