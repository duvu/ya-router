package yarouter

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	controlpkg "github.com/duvu/ya-router/internal/control"
)

func TestChatModel_BeginUserMessageAppendsAndClearsComposer(t *testing.T) {
	m := newChatModel()
	m.composer = "hello there"
	m.cursor = len([]rune(m.composer))

	text := m.beginUserMessage()
	if text != "hello there" {
		t.Fatalf("returned text = %q", text)
	}
	if m.composer != "" || m.cursor != 0 {
		t.Fatalf("composer not cleared: composer=%q cursor=%d", m.composer, m.cursor)
	}
	if len(m.transcript) != 1 || m.transcript[0].Role != chatRoleUser || m.transcript[0].Text != "hello there" {
		t.Fatalf("transcript = %+v", m.transcript)
	}
	if m.state != chatAwaitingRoute {
		t.Fatalf("state = %v, want chatAwaitingRoute", m.state)
	}
}

func TestChatModel_RouteDeltaDoneAppendsOrderedAssistantText(t *testing.T) {
	m := newChatModel()
	m.beginUserMessage2("hi")
	m.onRoute(controlpkg.WSChatRoutePayload{Provider: "copilot", ResolvedModel: "gpt-5-mini"})
	m.onDelta(controlpkg.WSChatDeltaPayload{Text: "Hel"})
	m.onDelta(controlpkg.WSChatDeltaPayload{Text: "lo"})
	m.onDone()

	if m.selectedProvider != "" || m.selectedModel != "" {
		t.Fatalf("selection should clear on successful completion: provider=%q model=%q", m.selectedProvider, m.selectedModel)
	}
	if m.state != chatIdle {
		t.Fatalf("state = %v, want chatIdle", m.state)
	}
	if len(m.transcript) != 2 || m.transcript[1].Role != chatRoleAssistant || m.transcript[1].Text != "Hello" {
		t.Fatalf("transcript = %+v", m.transcript)
	}
	if m.transcript[1].Interrupted {
		t.Fatal("completed message must not be marked interrupted")
	}
}

// beginUserMessage2 is a tiny test helper that also seeds the composer so
// callers don't need to repeat the assignment.
func (m *chatModel) beginUserMessage2(text string) string {
	m.composer = text
	return m.beginUserMessage()
}

func TestChatModel_InterruptedChatIsMarkedNotResumed(t *testing.T) {
	m := newChatModel()
	m.beginUserMessage2("hi")
	m.onRoute(controlpkg.WSChatRoutePayload{Provider: "copilot", ResolvedModel: "gpt-5-mini"})
	m.onDelta(controlpkg.WSChatDeltaPayload{Text: "partial"})
	m.onError(true, "connection lost before this chat completed") // connection lost mid-stream

	if len(m.transcript) != 2 {
		t.Fatalf("transcript = %+v, want partial assistant entry retained", m.transcript)
	}
	if !m.transcript[1].Interrupted {
		t.Fatal("partial assistant entry must be marked interrupted")
	}
	if m.transcript[1].Text != "partial" {
		t.Fatalf("partial text = %q, want %q (never silently discarded)", m.transcript[1].Text, "partial")
	}
	if m.state != chatIdle {
		t.Fatalf("state = %v, want chatIdle after interruption", m.state)
	}
}

// TestChatModel_ErrorBeforeAnyDeltaDropsEmptyPlaceholder proves a chat.error
// that arrives before any text streamed does not leave an empty assistant
// bubble in the transcript.
func TestChatModel_ErrorBeforeAnyDeltaDropsEmptyPlaceholder(t *testing.T) {
	m := newChatModel()
	m.beginUserMessage2("hi")
	m.onRoute(controlpkg.WSChatRoutePayload{Provider: "copilot", ResolvedModel: "gpt-5-mini"})
	m.onError(false, "model_unavailable") // ordinary failure, not interrupted

	if len(m.transcript) != 1 {
		t.Fatalf("transcript = %+v, want only the user entry (empty assistant bubble dropped)", m.transcript)
	}
}

// TestChatModel_ErrorBeforeRouteIsStillVisible proves a routing failure that
// occurs before chat.route ever arrives (e.g. no active target) is still
// shown to the user, even though no assistant transcript bubble exists yet.
func TestChatModel_ErrorBeforeRouteIsStillVisible(t *testing.T) {
	m := newChatModel()
	m.beginUserMessage2("hi")
	m.onError(false, "no active target is available for model \"thiendu\"")

	if m.lastError == "" {
		t.Fatal("chat error before chat.route must still be visible via lastError")
	}
	if len(m.transcript) != 1 {
		t.Fatalf("transcript = %+v, want only the user entry", m.transcript)
	}
}

func TestChatModel_LastErrorClearsOnNextSuccess(t *testing.T) {
	m := newChatModel()
	m.beginUserMessage2("hi")
	m.onError(false, "boom")
	if m.lastError == "" {
		t.Fatal("expected lastError to be set")
	}
	m.beginUserMessage2("hi again")
	m.onRoute(controlpkg.WSChatRoutePayload{Provider: "copilot", ResolvedModel: "gpt-5-mini"})
	m.onDelta(controlpkg.WSChatDeltaPayload{Text: "ok"})
	m.onDone()
	if m.lastError != "" {
		t.Fatalf("lastError = %q, want cleared after a successful chat", m.lastError)
	}
}

func TestChatModel_CanSendRequiresIdleAndNonEmptyComposer(t *testing.T) {
	m := newChatModel()
	if m.canSend() {
		t.Fatal("empty composer must not be sendable")
	}
	m.composer = "   "
	if m.canSend() {
		t.Fatal("whitespace-only composer must not be sendable")
	}
	m.composer = "hi"
	if !m.canSend() {
		t.Fatal("non-empty composer while idle must be sendable")
	}
	m.state = chatStreaming
	if m.canSend() {
		t.Fatal("must not be sendable while a chat is in flight")
	}
}

func TestChatModel_ComposerEditingInsertsAtCursorAndSupportsBackspace(t *testing.T) {
	m := newChatModel()
	m.insertRune('h')
	m.insertRune('i')
	if m.composer != "hi" || m.cursor != 2 {
		t.Fatalf("composer=%q cursor=%d", m.composer, m.cursor)
	}
	m.moveCursorLeft()
	m.insertRune('!')
	if m.composer != "h!i" {
		t.Fatalf("composer = %q, want h!i", m.composer)
	}
	m.backspace()
	if m.composer != "hi" {
		t.Fatalf("composer after backspace = %q, want hi", m.composer)
	}
}

// TestChatModel_ComposerKeysTypeQJKNormally proves navigation-looking keys
// (q, j, k) are inserted as literal characters while the composer handles
// them — the workspace model is responsible for routing them here only when
// focused (see client_chat_workspace.go), and this test locks in that the
// composer itself never treats them specially.
func TestChatModel_ComposerKeysTypeQJKNormally(t *testing.T) {
	m := newChatModel()
	for _, r := range []rune{'q', 'j', 'k'} {
		m = m.handleComposerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.composer != "qjk" {
		t.Fatalf("composer = %q, want qjk", m.composer)
	}
}

func TestChatModel_AltEnterInsertsNewlineNotSubmit(t *testing.T) {
	m := newChatModel()
	m.composer = "line1"
	m.cursor = len([]rune(m.composer))
	m = m.handleComposerKey(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if m.composer != "line1\n" {
		t.Fatalf("composer = %q, want a literal newline appended", m.composer)
	}
}
