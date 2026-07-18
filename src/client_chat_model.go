// client_chat_model.go implements the chat half of the "ya" workspace
// (issue #79): a local user/assistant transcript, streaming assistant text,
// a multiline composer, and cancel/new-conversation controls. Chat state is
// held only in this process; the daemon never sees or persists it beyond one
// active chat.start per connection (see internal/control/ws_server.go).
package yarouter

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	controlpkg "github.com/duvu/ya-router/internal/control"
)

// chatMessageRole distinguishes local transcript entries; it is never sent
// to the daemon.
type chatMessageRole string

const (
	chatRoleUser      chatMessageRole = "user"
	chatRoleAssistant chatMessageRole = "assistant"
)

// chatTranscriptEntry is one local transcript line. Interrupted marks an
// assistant entry that never reached chat.done/chat.error because the
// connection dropped mid-stream; it is never resubmitted automatically.
type chatTranscriptEntry struct {
	Role        chatMessageRole
	Text        string
	Interrupted bool
}

type chatRequestState uint8

const (
	chatIdle chatRequestState = iota
	chatAwaitingRoute
	chatStreaming
)

// chatModel is the composable Bubble Tea sub-model for the chat pane. It has
// no network code of its own: the workspace model (client_chat_workspace.go)
// feeds it WSClient events and forwards its outgoing intents (send/cancel)
// back to the client.
type chatModel struct {
	transcript []chatTranscriptEntry
	composer   string
	cursor     int // rune index into composer

	state           chatRequestState
	activeRequestID string
	streamingIndex  int // index into transcript of the in-progress assistant entry

	requestedModel   string
	selectedProvider string
	selectedModel    string

	// lastError is a bounded, redacted description of the most recent
	// terminal chat.error, shown in the chat pane so a routing failure (no
	// active target, provider unavailable, etc.) is never silently
	// invisible just because no assistant text ever started streaming.
	lastError string

	focused bool
}

func newChatModel() chatModel {
	return chatModel{requestedModel: "thiendu"}
}

// nextChatRequestID returns a small monotonically-varying identifier. It
// does not need to be globally unique — only unique per connection, which a
// counter guarantees since one connection has one active chat at a time.
func nextChatRequestID(counter *int) string {
	*counter++
	return "chat-" + strconv.Itoa(*counter)
}

// canSend reports whether the composer has content and no chat is in
// flight — Enter/send is a no-op otherwise.
func (m chatModel) canSend() bool {
	return m.state == chatIdle && strings.TrimSpace(m.composer) != ""
}

// canCancel reports whether a cancel is meaningful right now.
func (m chatModel) canCancel() bool {
	return m.state != chatIdle
}

// beginUserMessage appends the composer's content as a user transcript entry
// and clears the composer. It does not itself send anything over the wire —
// callers pair it with WSClient.StartChat.
func (m *chatModel) beginUserMessage() string {
	text := strings.TrimSpace(m.composer)
	m.transcript = append(m.transcript, chatTranscriptEntry{Role: chatRoleUser, Text: text})
	m.composer = ""
	m.cursor = 0
	m.state = chatAwaitingRoute
	m.streamingIndex = -1
	return text
}

// onRoute records the selected provider/model once chat.route arrives.
func (m *chatModel) onRoute(route controlpkg.WSChatRoutePayload) {
	m.state = chatStreaming
	m.selectedProvider = route.Provider
	m.selectedModel = route.ResolvedModel
	m.transcript = append(m.transcript, chatTranscriptEntry{Role: chatRoleAssistant, Text: ""})
	m.streamingIndex = len(m.transcript) - 1
}

// onDelta appends one ordered text increment to the in-progress assistant
// entry. A delta that arrives before chat.route (should not happen, but
// defensively handled) starts one implicitly.
func (m *chatModel) onDelta(delta controlpkg.WSChatDeltaPayload) {
	if m.streamingIndex < 0 || m.streamingIndex >= len(m.transcript) {
		m.transcript = append(m.transcript, chatTranscriptEntry{Role: chatRoleAssistant})
		m.streamingIndex = len(m.transcript) - 1
	}
	m.transcript[m.streamingIndex].Text += delta.Text
}

// onDone finalizes a successful chat.
func (m *chatModel) onDone() {
	m.lastError = ""
	m.finishRequest(false)
}

// onError finalizes a failed/canceled/interrupted chat. If no assistant text
// had started streaming yet, the placeholder entry is dropped rather than
// leaving an empty bubble; message is always recorded in lastError so a
// routing failure that never reached chat.route is still visible.
func (m *chatModel) onError(interrupted bool, message string) {
	if m.streamingIndex >= 0 && m.streamingIndex < len(m.transcript) {
		if m.transcript[m.streamingIndex].Text == "" && !interrupted {
			m.transcript = append(m.transcript[:m.streamingIndex], m.transcript[m.streamingIndex+1:]...)
		} else {
			m.transcript[m.streamingIndex].Interrupted = interrupted
		}
	}
	m.lastError = message
	m.finishRequest(interrupted)
}

func (m *chatModel) finishRequest(interrupted bool) {
	m.state = chatIdle
	m.activeRequestID = ""
	m.streamingIndex = -1
	if !interrupted {
		m.selectedProvider = ""
		m.selectedModel = ""
	}
}

// insertRune inserts r at the current cursor position.
func (m *chatModel) insertRune(r rune) {
	runes := []rune(m.composer)
	runes = append(runes[:m.cursor], append([]rune{r}, runes[m.cursor:]...)...)
	m.composer = string(runes)
	m.cursor++
}

func (m *chatModel) insertNewline() { m.insertRune('\n') }

func (m *chatModel) backspace() {
	if m.cursor == 0 {
		return
	}
	runes := []rune(m.composer)
	runes = append(runes[:m.cursor-1], runes[m.cursor:]...)
	m.composer = string(runes)
	m.cursor--
}

func (m *chatModel) moveCursorLeft() {
	if m.cursor > 0 {
		m.cursor--
	}
}

func (m *chatModel) moveCursorRight() {
	if m.cursor < len([]rune(m.composer)) {
		m.cursor++
	}
}

// handleComposerKey processes one keypress while the composer has focus. It
// never intercepts the workspace's global quit/tab-switch keys (see
// client_chat_workspace.go), only composer-local editing and submission.
func (m chatModel) handleComposerKey(key tea.KeyMsg) chatModel {
	switch key.Type {
	case tea.KeyBackspace:
		m.backspace()
	case tea.KeyLeft:
		m.moveCursorLeft()
	case tea.KeyRight:
		m.moveCursorRight()
	case tea.KeyEnter:
		if key.Alt {
			m.insertNewline()
		}
		// Plain Enter is handled by the workspace (submits the message);
		// alt+enter/shift+enter inserts a literal newline.
	case tea.KeyCtrlJ:
		m.insertNewline()
	case tea.KeyRunes:
		if !key.Alt && !key.Paste {
			for _, r := range key.Runes {
				m.insertRune(r)
			}
		}
	case tea.KeySpace:
		m.insertRune(' ')
	}
	return m
}
