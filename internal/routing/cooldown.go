package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/duvu/ya-router/internal/availability"
	"github.com/duvu/ya-router/internal/provider"
)

const (
	baseCooldown  = 15 * time.Second
	maxCooldown   = 5 * time.Minute
	quotaCooldown = 24 * time.Hour

	cooldownStateVersion = 1
	maxPersistedCooldowns = 4096
)

type cooldownEntry struct {
	providerID provider.ID
	model      string
	capability provider.Capability
	until      time.Time
	reason     availability.CooldownReason
	failures   int
}

type persistedCooldownState struct {
	Version int                      `json:"version"`
	Entries []persistedCooldownEntry `json:"entries"`
}

type persistedCooldownEntry struct {
	ProviderID string                      `json:"provider"`
	Model      string                      `json:"model"`
	Capability string                      `json:"capability"`
	Until      time.Time                   `json:"cooldown_until"`
	Reason     availability.CooldownReason `json:"reason"`
	Failures   int                         `json:"failures,omitempty"`
}

// CooldownRegistry stores bounded target feedback independently from immutable
// runtime snapshots. Snapshot builders copy its current state before routing.
// When statePath is configured, entries are persisted atomically so quota
// cooldowns survive daemon restart and configuration hot reload.
type CooldownRegistry struct {
	mu        sync.Mutex
	now       func() time.Time
	entries   map[string]cooldownEntry
	statePath string
}

func NewCooldownRegistry() *CooldownRegistry {
	return &CooldownRegistry{now: time.Now, entries: make(map[string]cooldownEntry)}
}

// OpenCooldownRegistry opens a registry backed by an owner-only JSON state file.
// Invalid JSON or invalid individual entries are ignored with sanitized logs so
// corrupted advisory routing state cannot prevent the daemon from starting.
func OpenCooldownRegistry(statePath string) (*CooldownRegistry, error) {
	registry := NewCooldownRegistry()
	statePath = strings.TrimSpace(statePath)
	if statePath == "" {
		return registry, nil
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return nil, fmt.Errorf("create cooldown state directory: %w", err)
	}
	registry.statePath = statePath
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return registry, nil
		}
		return nil, fmt.Errorf("read cooldown state: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return registry, nil
	}
	var state persistedCooldownState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("[cooldown] ignoring invalid persisted cooldown state")
		return registry, nil
	}
	if state.Version != cooldownStateVersion {
		log.Printf("[cooldown] ignoring unsupported persisted cooldown state version")
		return registry, nil
	}
	now := registry.now().UTC()
	for index, item := range state.Entries {
		if index >= maxPersistedCooldowns {
			break
		}
		entry, ok := decodePersistedCooldown(item, now)
		if !ok {
			continue
		}
		registry.entries[cooldownEntryKey(entry.providerID, entry.model, entry.capability)] = entry
	}
	return registry, nil
}

func decodePersistedCooldown(item persistedCooldownEntry, now time.Time) (cooldownEntry, bool) {
	providerID := provider.ID(strings.TrimSpace(item.ProviderID))
	model := strings.TrimSpace(item.Model)
	capability := provider.Capability(strings.TrimSpace(item.Capability))
	if providerID == "" || model == "" || capability == "" || !validCooldownReason(item.Reason) {
		return cooldownEntry{}, false
	}
	until := item.Until.UTC()
	if until.IsZero() || !until.After(now) {
		return cooldownEntry{}, false
	}
	failures := item.Failures
	if failures < 1 {
		failures = 1
	}
	return cooldownEntry{
		providerID: providerID,
		model:      model,
		capability: capability,
		until:      until,
		reason:     item.Reason,
		failures:   failures,
	}, true
}

func validCooldownReason(reason availability.CooldownReason) bool {
	switch reason {
	case availability.CooldownRateLimited,
		availability.CooldownEntitlementDenied,
		availability.CooldownAuthRequired,
		availability.CooldownTransientFailure,
		availability.CooldownTimeout,
		availability.CooldownQuotaExhausted:
		return true
	default:
		return false
	}
}

func cooldownEntryKey(providerID provider.ID, model string, capability provider.Capability) string {
	return string(providerID) + "\x00" + string(capability) + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

func (registry *CooldownRegistry) Record(providerID provider.ID, model string, capability provider.Capability, reason availability.CooldownReason, hint time.Duration) (time.Time, bool) {
	if registry == nil || providerID == "" || strings.TrimSpace(model) == "" || capability == "" || !validCooldownReason(reason) {
		return time.Time{}, false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.now().UTC()
	key := cooldownEntryKey(providerID, model, capability)
	existing, exists := registry.entries[key]
	failures := 1
	if exists && existing.until.After(now) {
		failures = existing.failures + 1
	}
	duration := cooldownDuration(reason, failures, hint)
	until := now.Add(duration)
	if exists && existing.until.After(until) {
		until = existing.until
	}
	registry.entries[key] = cooldownEntry{
		providerID: providerID,
		model:      strings.TrimSpace(model),
		capability: capability,
		until:      until,
		reason:     reason,
		failures:   failures,
	}
	registry.persistOrLogLocked()
	return until, true
}

func cooldownDuration(reason availability.CooldownReason, failures int, hint time.Duration) time.Duration {
	if reason == availability.CooldownQuotaExhausted {
		duration := quotaCooldown
		if hint > duration {
			duration = hint
		}
		return duration
	}
	duration := baseCooldown
	for step := 1; step < failures && duration < maxCooldown; step++ {
		duration *= 2
	}
	if duration > maxCooldown {
		duration = maxCooldown
	}
	if hint > 0 {
		if hint > maxCooldown {
			hint = maxCooldown
		}
		if hint > duration {
			duration = hint
		}
	}
	return duration
}

func (registry *CooldownRegistry) Clear(providerID provider.ID, model string, capability provider.Capability) bool {
	if registry == nil {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := cooldownEntryKey(providerID, model, capability)
	if _, exists := registry.entries[key]; !exists {
		return false
	}
	delete(registry.entries, key)
	registry.persistOrLogLocked()
	return true
}

func (registry *CooldownRegistry) Cooldowns(providerID provider.ID) (active []availability.Cooldown, expired []availability.Cooldown) {
	if registry == nil {
		return nil, nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.now().UTC()
	changed := false
	for key, entry := range registry.entries {
		if entry.providerID != providerID {
			continue
		}
		cooldown := availability.Cooldown{Model: entry.model, Capability: entry.capability, Until: entry.until, Reason: entry.reason}
		if entry.until.After(now) {
			active = append(active, cooldown)
			continue
		}
		delete(registry.entries, key)
		changed = true
		expired = append(expired, cooldown)
	}
	if changed {
		registry.persistOrLogLocked()
	}
	return active, expired
}

func (registry *CooldownRegistry) persistOrLogLocked() {
	if registry == nil || registry.statePath == "" {
		return
	}
	if err := registry.persistLocked(); err != nil {
		log.Printf("[cooldown] failed to persist cooldown state: %v", err)
	}
}

func (registry *CooldownRegistry) persistLocked() error {
	entries := make([]persistedCooldownEntry, 0, len(registry.entries))
	for _, entry := range registry.entries {
		entries = append(entries, persistedCooldownEntry{
			ProviderID: string(entry.providerID),
			Model:      entry.model,
			Capability: string(entry.capability),
			Until:      entry.until.UTC(),
			Reason:     entry.reason,
			Failures:   entry.failures,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ProviderID != entries[j].ProviderID {
			return entries[i].ProviderID < entries[j].ProviderID
		}
		if entries[i].Capability != entries[j].Capability {
			return entries[i].Capability < entries[j].Capability
		}
		return strings.ToLower(entries[i].Model) < strings.ToLower(entries[j].Model)
	})
	data, err := json.MarshalIndent(persistedCooldownState{Version: cooldownStateVersion, Entries: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(registry.statePath)
	temp, err := os.CreateTemp(dir, ".cooldowns-*")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("secure temporary state: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temporary state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary state: %w", err)
	}
	if err := os.Rename(tempPath, registry.statePath); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	if err := os.Chmod(registry.statePath, 0o600); err != nil {
		return fmt.Errorf("secure state: %w", err)
	}
	return nil
}

func cooldownReason(status int, err error) (availability.CooldownReason, bool) {
	if errors.Is(err, context.Canceled) {
		return "", false
	}
	if errors.Is(err, context.DeadlineExceeded) || status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout {
		return availability.CooldownTimeout, true
	}
	switch status {
	case http.StatusTooManyRequests:
		return availability.CooldownRateLimited, true
	case http.StatusUnauthorized:
		return availability.CooldownAuthRequired, true
	case http.StatusPaymentRequired, http.StatusForbidden:
		return availability.CooldownEntitlementDenied, true
	}
	if status >= http.StatusInternalServerError || err != nil {
		return availability.CooldownTransientFailure, true
	}
	return "", false
}

func cooldownPolicy(status int, err error, retryAfter time.Duration) (availability.CooldownReason, time.Duration, bool) {
	reason, shouldCooldown := cooldownReason(status, err)
	if !shouldCooldown {
		return "", 0, false
	}
	switch status {
	case http.StatusPaymentRequired, http.StatusForbidden:
		return availability.CooldownQuotaExhausted, retryAfter, true
	case http.StatusTooManyRequests:
		// A short Retry-After is treated as burst throttling. A 429 without a
		// short reset signal is treated as usage-quota exhaustion and held for
		// the fixed 24-hour target cooldown.
		if retryAfter > 0 && retryAfter <= maxCooldown {
			return availability.CooldownRateLimited, retryAfter, true
		}
		return availability.CooldownQuotaExhausted, retryAfter, true
	default:
		return reason, retryAfter, true
	}
}

// RecordOutcome consumes a selected route's redacted result after dispatch.
// It affects later automatic-routing requests and the same request's bounded
// sequential failover selection.
func (router *Router) RecordOutcome(decision *SelectionDecision, status int, err error, retryAfter time.Duration) {
	if router == nil || decision == nil {
		return
	}
	bare, providerID, hasPrefix := StripModelPrefix(decision.SelectedTarget)
	if !hasPrefix {
		return
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices && err == nil {
		if router.cooldowns.Clear(providerID, bare, decision.Capability) {
			router.metrics.RecordCooldownExit(decision.SelectedTarget, "recovered")
		}
		return
	}
	reason, hint, shouldCooldown := cooldownPolicy(status, err, retryAfter)
	if !shouldCooldown {
		return
	}
	if _, entered := router.cooldowns.Record(providerID, bare, decision.Capability, reason, hint); entered {
		router.metrics.RecordCooldownEntry(decision.SelectedTarget, reason)
	}
}
