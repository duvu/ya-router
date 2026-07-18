package yarouter

import (
	"net/http"
	"testing"
)

func TestWSChatWriter_ExtractsOrderedStreamingDeltas(t *testing.T) {
	var deltas []string
	w := newWSChatWriter(func(text string) { deltas = append(deltas, text) })
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	chunks := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\", world\"}}]}\n\n",
		"data: [DONE]\n\n",
	}
	for _, chunk := range chunks {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	w.Finish()

	want := []string{"Hel", "lo", ", world"}
	if len(deltas) != len(want) {
		t.Fatalf("deltas = %v, want %v", deltas, want)
	}
	for i := range want {
		if deltas[i] != want[i] {
			t.Fatalf("delta[%d] = %q, want %q", i, deltas[i], want[i])
		}
	}
}

// TestWSChatWriter_HandlesChunkSplitAcrossWrites proves a chunk split across
// two Write calls (as happens with real network I/O) is still parsed intact
// with no lost or duplicated UTF-8 content.
func TestWSChatWriter_HandlesChunkSplitAcrossWrites(t *testing.T) {
	var deltas []string
	w := newWSChatWriter(func(text string) { deltas = append(deltas, text) })
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	full := "data: {\"choices\":[{\"delta\":{\"content\":\"héllo\"}}]}\n\n"
	mid := len(full) / 2
	if _, err := w.Write([]byte(full[:mid])); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(full[mid:])); err != nil {
		t.Fatal(err)
	}

	if len(deltas) != 1 || deltas[0] != "héllo" {
		t.Fatalf("deltas = %v, want [héllo]", deltas)
	}
}

func TestWSChatWriter_NonStreamingResponseYieldsOneDeltaOnFinish(t *testing.T) {
	var deltas []string
	w := newWSChatWriter(func(text string) { deltas = append(deltas, text) })
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(`{"id":"x","choices":[{"message":{"content":"full answer"}}]}`)); err != nil {
		t.Fatal(err)
	}
	text := w.Finish()
	if text != "full answer" {
		t.Fatalf("Finish() = %q, want %q", text, "full answer")
	}
	if len(deltas) != 1 || deltas[0] != "full answer" {
		t.Fatalf("deltas = %v", deltas)
	}
}

func TestWSChatWriter_StatusCodeDefaultsToOK(t *testing.T) {
	w := newWSChatWriter(nil)
	if w.StatusCode() != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200 before any write", w.StatusCode())
	}
	w.WriteHeader(http.StatusTooManyRequests)
	if w.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want 429", w.StatusCode())
	}
}

func TestWSChatWriter_MalformedSSELineIsIgnoredNotFatal(t *testing.T) {
	var deltas []string
	w := newWSChatWriter(func(text string) { deltas = append(deltas, text) })
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("data: {not valid json\n\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")); err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 1 || deltas[0] != "ok" {
		t.Fatalf("deltas = %v, want [ok] (malformed line must not break subsequent parsing)", deltas)
	}
}
