// telemetry_runtime.go wires the daemon-lifetime telemetry recorder into the
// data plane. Follows the same package-level accessor pattern as
// currentConfigState() (see revisioned_state.go): daemon startup sets it
// once, and unset callers (compatibility binary, most tests) safely no-op
// since telemetrypkg.Recorder's methods are nil-safe.
package yarouter

import (
	"context"
	"errors"
	"sync"

	telemetrypkg "github.com/duvu/ya-router/internal/telemetry"
)

var telemetryState struct {
	sync.RWMutex
	recorder *telemetrypkg.Recorder
}

// currentTelemetryRecorder returns the active telemetry recorder, or nil when
// none has been set. Every telemetrypkg.Recorder/InFlight method tolerates a
// nil receiver, so callers never need to branch on this being unset.
func currentTelemetryRecorder() *telemetrypkg.Recorder {
	telemetryState.RLock()
	defer telemetryState.RUnlock()
	return telemetryState.recorder
}

// setTelemetryRecorder installs the daemon-lifetime telemetry recorder.
func setTelemetryRecorder(recorder *telemetrypkg.Recorder) {
	telemetryState.Lock()
	telemetryState.recorder = recorder
	telemetryState.Unlock()
}

// classifyErrorCategory maps a terminal proxy outcome to the bounded
// telemetry.ErrorCategory set. It never surfaces an upstream error string:
// only the stable ProviderErrorKind (when available) or HTTP status class.
func classifyErrorCategory(status int, err error) telemetrypkg.ErrorCategory {
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return telemetrypkg.ErrorCategoryTimeout
		}
		if errors.Is(err, context.Canceled) {
			return telemetrypkg.ErrorCategoryCanceled
		}
		if kind, ok := providerErrorKind(err); ok {
			switch kind {
			case ProviderErrorInvalidRequest:
				return telemetrypkg.ErrorCategoryInvalidRequest
			case ProviderErrorAuthRequired:
				return telemetrypkg.ErrorCategoryAuthRequired
			case ProviderErrorEntitlement:
				return telemetrypkg.ErrorCategoryEntitlementDenied
			case ProviderErrorRateLimit:
				return telemetrypkg.ErrorCategoryRateLimited
			case ProviderErrorUnsupported:
				return telemetrypkg.ErrorCategoryUnsupported
			case ProviderErrorUnavailable:
				return telemetrypkg.ErrorCategoryProviderUnavailable
			case ProviderErrorModelUnavailable:
				return telemetrypkg.ErrorCategoryModelUnavailable
			case ProviderErrorTransport:
				return telemetrypkg.ErrorCategoryTransport
			}
		}
	}
	switch {
	case status == 429:
		return telemetrypkg.ErrorCategoryRateLimited
	case status == 401 || status == 403:
		return telemetrypkg.ErrorCategoryAuthRequired
	case status == 408 || status == 504:
		return telemetrypkg.ErrorCategoryTimeout
	case status == 503:
		return telemetrypkg.ErrorCategoryProviderUnavailable
	case status >= 500:
		return telemetrypkg.ErrorCategoryProviderUnavailable
	case status >= 400:
		return telemetrypkg.ErrorCategoryInvalidRequest
	default:
		return telemetrypkg.ErrorCategoryTransport
	}
}

func providerErrorKind(err error) (ProviderErrorKind, bool) {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Kind, true
	}
	return "", false
}
