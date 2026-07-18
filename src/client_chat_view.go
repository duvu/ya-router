// client_chat_view.go renders the chat transcript/composer and the compact
// status sidebar for the "ya" workspace (issue #79). Rendering is plain text
// with bracketed markers rather than color-only signaling, so status is
// legible without ANSI color support.
package yarouter

import (
	"fmt"
	"strings"

	controlpkg "github.com/duvu/ya-router/internal/control"
)

func (m workspaceModel) renderChat() string {
	var out strings.Builder
	out.WriteString("Chat")
	if m.chat.selectedProvider != "" {
		fmt.Fprintf(&out, "  (routed to %s/%s)", m.chat.selectedProvider, m.chat.selectedModel)
	}
	out.WriteString("\n")

	if len(m.chat.transcript) == 0 {
		out.WriteString("  Send a message to start a thiendu chat.\n")
	}
	for _, entry := range m.chat.transcript {
		out.WriteString(renderChatEntry(entry))
	}
	if m.chat.state != chatIdle {
		out.WriteString("  ...\n")
	}
	if m.chat.lastError != "" {
		fmt.Fprintf(&out, "  [error] %s\n", m.chat.lastError)
	}
	out.WriteString("\n")
	out.WriteString(renderComposer(m.chat))
	return out.String()
}

func renderChatEntry(entry chatTranscriptEntry) string {
	label := "you"
	if entry.Role == chatRoleAssistant {
		label = "thiendu"
	}
	text := entry.Text
	if entry.Interrupted {
		text += " [interrupted — connection lost before this reply finished]"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "  %s: ", label)
	lines := strings.Split(text, "\n")
	out.WriteString(lines[0])
	out.WriteString("\n")
	for _, line := range lines[1:] {
		out.WriteString("      " + line + "\n")
	}
	return out.String()
}

func renderComposer(chat chatModel) string {
	border := "----------------------------------------\n"
	focusMarker := "composer"
	if chat.focused {
		focusMarker = "composer [focused]"
	}
	var out strings.Builder
	out.WriteString(border)
	fmt.Fprintf(&out, "%s: %s\n", focusMarker, composerWithCursor(chat.composer, chat.cursor, chat.focused))
	return out.String()
}

func composerWithCursor(text string, cursor int, focused bool) string {
	if !focused {
		if text == "" {
			return "(press i to type)"
		}
		return text
	}
	runes := []rune(text)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	return string(runes[:cursor]) + "|" + string(runes[cursor:])
}

func (m workspaceModel) renderStatus() string {
	state := m.dashboard.snapshot.wsState
	var out strings.Builder
	out.WriteString("Status\n")
	writeProviderStatusLines(&out, state.Providers)
	out.WriteString("\n")
	writeRoutingStatusLines(&out, state.Routing)
	out.WriteString("\n")
	writeCounterStatusLines(&out, state.Counters)
	return out.String()
}

func writeProviderStatusLines(out *strings.Builder, providers []controlpkg.ProviderStateView) {
	out.WriteString("Providers\n")
	if len(providers) == 0 {
		out.WriteString("  No live provider state yet.\n")
		return
	}
	for _, provider := range providers {
		fmt.Fprintf(out, "  [%s] %s\n", provider.State, provider.Provider)
	}
}

func writeRoutingStatusLines(out *strings.Builder, routing []controlpkg.RoutingStateView) {
	out.WriteString("Routing\n")
	if len(routing) == 0 {
		out.WriteString("  No routing state yet.\n")
		return
	}
	for _, vm := range routing {
		selected := vm.SelectedTarget
		if selected == "" {
			selected = "no eligible target"
		}
		fmt.Fprintf(out, "  %s -> %s\n", vm.VirtualModel, selected)
		for _, target := range vm.Targets {
			marker := "unavailable"
			if target.Routable {
				marker = "ready"
			}
			fmt.Fprintf(out, "    %s [%s:%s]\n", target.Target, marker, target.Reason)
		}
	}
}

func writeCounterStatusLines(out *strings.Builder, counters []controlpkg.TargetCountersView) {
	out.WriteString("Usage (since daemon start)\n")
	if len(counters) == 0 {
		out.WriteString("  No requests yet.\n")
		return
	}
	for _, counter := range counters {
		tokens := "unavailable"
		if !counter.Usage.Unavailable {
			tokens = fmt.Sprintf("%d", counter.Usage.TotalTokens)
		}
		fmt.Fprintf(out, "  %s/%s req=%d ok=%d err=%d inflight=%d msgs=%d tokens=%s\n",
			counter.Provider, counter.Model, counter.Requests, counter.Successes, counter.Errors, counter.InFlight, counter.Messages, tokens)
		if counter.LastErrorCategory != "" {
			fmt.Fprintf(out, "    last_error=%s latency_ms=%d\n", counter.LastErrorCategory, counter.LastLatencyMillis)
		}
	}
}

// padColumnsSideBySide lays out left/right blocks of text side by side,
// padding each line to a fixed column width so a shorter block's lines
// don't creep under the taller block's content.
func padColumnsSideBySide(left, right string, leftWidth, rightWidth int) string {
	leftLines := strings.Split(strings.TrimRight(left, "\n"), "\n")
	rightLines := strings.Split(strings.TrimRight(right, "\n"), "\n")
	rows := len(leftLines)
	if len(rightLines) > rows {
		rows = len(rightLines)
	}
	var out strings.Builder
	for i := 0; i < rows; i++ {
		var l, r string
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		fmt.Fprintf(&out, "%-*s | %-*s\n", leftWidth, padOrTruncate(l, leftWidth), rightWidth, padOrTruncate(r, rightWidth))
	}
	return out.String()
}

func padOrTruncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) > width {
		if width <= 1 {
			return string(runes[:width])
		}
		return string(runes[:width-1]) + "…"
	}
	return s
}
