package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"sync"
	"time"
)

// StateHub tracks every live /control/v1/ws connection and pushes
// state.updated when the daemon's compact state changes. It performs no
// provider/routing I/O itself — that is StateReader's job — and publication
// failure here never blocks routing or inference: BroadcastNow only calls
// the non-blocking WSConn.Send on already-established connections.
type StateHub struct {
	reader StateReader

	mu    sync.Mutex
	conns map[*WSConn]struct{}

	fingerprintMu sync.Mutex
	fingerprint   string
}

// NewStateHub returns a hub that reads state through reader. A nil reader
// makes every broadcast a no-op, so callers may construct a hub before the
// real read model exists.
func NewStateHub(reader StateReader) *StateHub {
	return &StateHub{reader: reader, conns: make(map[*WSConn]struct{})}
}

func (hub *StateHub) register(conn *WSConn) {
	if hub == nil {
		return
	}
	hub.mu.Lock()
	hub.conns[conn] = struct{}{}
	hub.mu.Unlock()
}

func (hub *StateHub) unregister(conn *WSConn) {
	if hub == nil {
		return
	}
	hub.mu.Lock()
	delete(hub.conns, conn)
	hub.mu.Unlock()
}

func (hub *StateHub) snapshot() []*WSConn {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	out := make([]*WSConn, 0, len(hub.conns))
	for conn := range hub.conns {
		out = append(out, conn)
	}
	return out
}

// BroadcastNow reads current state and, only if it differs from the last
// broadcast, sends one state.updated to every currently connected client. It
// never triggers a fresh per-client fetch: all connections receive the same
// polled payload, keeping broadcast cost independent of connection count
// beyond the fan-out itself.
func (hub *StateHub) BroadcastNow(ctx context.Context) {
	if hub == nil || hub.reader == nil {
		return
	}
	payload, err := hub.reader.State(ctx)
	if err != nil {
		log.Printf("control ws: state read failed, skipping broadcast: %v", err)
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Printf("control ws: encode state for fingerprint: %v", err)
		return
	}
	digest := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(digest[:])

	hub.fingerprintMu.Lock()
	unchanged := fingerprint == hub.fingerprint
	hub.fingerprint = fingerprint
	hub.fingerprintMu.Unlock()
	if unchanged {
		return
	}
	for _, conn := range hub.snapshot() {
		conn.Send(WSTypeStateUpdated, "", json.RawMessage(encoded))
	}
}

// StartPolling runs BroadcastNow every interval until ctx is canceled or the
// returned stop function is called; stop blocks until the polling goroutine
// has exited, so daemon shutdown never leaves it running.
func (hub *StateHub) StartPolling(ctx context.Context, interval time.Duration) (stop func()) {
	if hub == nil {
		return func() {}
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				hub.BroadcastNow(loopCtx)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
