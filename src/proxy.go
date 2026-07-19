// proxy.go — HTTP proxy infrastructure (circuit breaker, retry, worker pool,
// request coalescing) and the provider-dispatching request handler.
package yarouter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	requestproxy "github.com/duvu/ya-router/internal/proxy"
	routingpkg "github.com/duvu/ya-router/internal/routing"
	telemetrypkg "github.com/duvu/ya-router/internal/telemetry"
)

const (
	copilotAPIBase = "https://api.githubcopilot.com"

	maxChatRetries     = 3
	baseChatRetryDelay = 1 // seconds

	circuitBreakerFailureThreshold = 5
)

// CircuitBreakerState represents the state of the circuit breaker.
type CircuitBreakerState int

const (
	CircuitClosed   CircuitBreakerState = iota
	CircuitOpen                         // too many failures; rejecting requests
	CircuitHalfOpen                     // testing if upstream has recovered
)

// CircuitBreaker guards against cascading failures to an upstream endpoint.
// Each provider holds its own instance.
type CircuitBreaker struct {
	failureCount    int64
	lastFailureTime time.Time
	state           CircuitBreakerState
	timeout         time.Duration
	probeInFlight   bool
	mutex           sync.Mutex
}

func (cb *CircuitBreaker) canExecute() bool {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.state = CircuitHalfOpen
			cb.probeInFlight = true
			return true
		}
		return false
	case CircuitHalfOpen:
		if cb.probeInFlight {
			return false
		}
		cb.probeInFlight = true
		return true
	default:
		return false
	}
}

func (cb *CircuitBreaker) onSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.failureCount = 0
	cb.state = CircuitClosed
	cb.probeInFlight = false
}

func (cb *CircuitBreaker) onFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	if cb.state == CircuitHalfOpen {
		cb.failureCount = circuitBreakerFailureThreshold
	} else {
		cb.failureCount++
	}
	cb.lastFailureTime = time.Now()
	cb.probeInFlight = false
	if cb.failureCount >= circuitBreakerFailureThreshold {
		cb.state = CircuitOpen
	}
}

// sharedHTTPClient is initialised once via initializeTimeouts.
var sharedHTTPClient *http.Client

// initializeTimeouts configures sharedHTTPClient from cfg.Timeouts.
func initializeTimeouts(cfg *Config) {
	sharedHTTPClient = &http.Client{
		Timeout: time.Duration(cfg.Timeouts.HTTPClient) * time.Second,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     time.Duration(cfg.Timeouts.IdleConnTimeout) * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   time.Duration(cfg.Timeouts.DialTimeout) * time.Second,
				KeepAlive: time.Duration(cfg.Timeouts.KeepAlive) * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: time.Duration(cfg.Timeouts.TLSHandshake) * time.Second,
		},
	}
}

// bufferPool reuses 32 KB slices for response copying.
var bufferPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 32*1024)
		return &b
	},
}

// WorkerPool dispatches jobs across a fixed goroutine pool.
type WorkerPool struct {
	workers        int
	jobQueue       chan func()
	spaceAvailable chan struct{}
	quit           chan struct{}
	wg             sync.WaitGroup
	stopOnce       sync.Once
	stateMu        sync.Mutex
	stopped        bool
}

func NewWorkerPool(workers int) *WorkerPool {
	if workers <= 0 {
		workers = runtime.NumCPU() * 2
	}
	wp := &WorkerPool{
		workers:        workers,
		jobQueue:       make(chan func(), workers*2),
		spaceAvailable: make(chan struct{}, 1),
		quit:           make(chan struct{}),
	}
	wp.start()
	return wp
}

func (wp *WorkerPool) start() {
	for i := 0; i < wp.workers; i++ {
		wp.wg.Add(1)
		go func() {
			defer wp.wg.Done()
			for {
				select {
				case job := <-wp.jobQueue:
					select {
					case wp.spaceAvailable <- struct{}{}:
					default:
					}
					if job != nil {
						job()
					}
				case <-wp.quit:
					return
				}
			}
		}()
	}
}

func (wp *WorkerPool) Submit(job func()) bool {
	return wp.SubmitContext(context.Background(), job)
}

// SubmitContext applies bounded backpressure and stops waiting when the caller
// is cancelled. No job is accepted after Stop begins.
func (wp *WorkerPool) SubmitContext(ctx context.Context, job func()) bool {
	if wp == nil || job == nil {
		return false
	}
	for {
		wp.stateMu.Lock()
		if wp.stopped {
			wp.stateMu.Unlock()
			return false
		}
		select {
		case wp.jobQueue <- job:
			wp.stateMu.Unlock()
			return true
		default:
			wp.stateMu.Unlock()
		}
		select {
		case <-ctx.Done():
			return false
		case <-wp.quit:
			return false
		case <-wp.spaceAvailable:
		}
	}
}

func (wp *WorkerPool) Stop() {
	if wp == nil {
		return
	}
	wp.stopOnce.Do(func() {
		wp.stateMu.Lock()
		wp.stopped = true
		close(wp.quit)
		wp.stateMu.Unlock()
	})
	wp.wg.Wait()
}

// coalescingEntry holds a pending or completed coalesced request.
type coalescingEntry struct {
	done   chan struct{}
	result interface{}
}

// CoalescingCache collapses identical concurrent requests into one upstream call.
type CoalescingCache struct {
	requests map[string]*coalescingEntry
	mutex    sync.Mutex
}

func NewCoalescingCache() *CoalescingCache {
	return &CoalescingCache{requests: make(map[string]*coalescingEntry)}
}

func (cc *CoalescingCache) getRequestKey(method, url string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte(url))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func (cc *CoalescingCache) CoalesceRequest(key string, fn func() interface{}) interface{} {
	cc.mutex.Lock()
	if entry, exists := cc.requests[key]; exists {
		cc.mutex.Unlock()
		<-entry.done
		return entry.result
	}
	entry := &coalescingEntry{done: make(chan struct{})}
	cc.requests[key] = entry
	cc.mutex.Unlock()

	entry.result = fn()
	close(entry.done)

	cc.mutex.Lock()
	delete(cc.requests, key)
	cc.mutex.Unlock()
	return entry.result
}

// isRetriableError returns true for transient HTTP/network errors.
// Account quota errors are handled by provider-specific account failover.
func isRetriableError(statusCode int, err error) bool {
	if err != nil {
		return true
	}
	return statusCode >= 500 || statusCode == http.StatusRequestTimeout
}

// makeRequestWithRetry executes a request with bounded retries. Unsafe methods
// are retried only when the caller supplies an Idempotency-Key, preventing a
// duplicate model generation after an uncertain delivery.
func makeRequestWithRetry(client *http.Client, req *http.Request, body []byte) (*http.Response, error) {
	var lastResp *http.Response
	var lastErr error
	ctx := req.Context()
	safeMethod := req.Method == http.MethodGet || req.Method == http.MethodHead
	retryAllowed := safeMethod || req.Header.Get("Idempotency-Key") != ""

	for attempt := 1; attempt <= maxChatRetries; attempt++ {
		retryReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), bytes.NewBuffer(body))
		if err != nil {
			return nil, err
		}
		for key, values := range req.Header {
			for _, value := range values {
				retryReq.Header.Add(key, value)
			}
		}
		log.Printf("Upstream attempt %d/%d → %s %s", attempt, maxChatRetries, retryReq.Method, retryReq.URL.String())
		started := time.Now()
		resp, err := client.Do(retryReq)
		elapsed := time.Since(started)
		if err != nil {
			lastErr = err
			log.Printf("Upstream attempt %d/%d failed after %s: %v", attempt, maxChatRetries, elapsed, err)
			if !retryAllowed || attempt == maxChatRetries {
				return nil, err
			}
			backoff := time.Duration(baseChatRetryDelay*attempt*attempt) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}

		lastResp = resp
		log.Printf("Upstream attempt %d/%d → HTTP %d (%s, Content-Type: %s)",
			attempt, maxChatRetries, resp.StatusCode, elapsed, resp.Header.Get("Content-Type"))
		if !isRetriableError(resp.StatusCode, nil) || !retryAllowed || attempt == maxChatRetries {
			return resp, nil
		}
		resp.Body.Close()
		backoff := time.Duration(baseChatRetryDelay*attempt*attempt) * time.Second
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}
	return lastResp, lastErr
}

// responseWrapper tracks committed status and response size.
type responseWrapper struct {
	http.ResponseWriter
	headersSent  bool
	statusCode   int
	bytesWritten int64
}

func (rw *responseWrapper) WriteHeader(statusCode int) {
	if !rw.headersSent {
		rw.headersSent = true
		rw.statusCode = statusCode
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

func (rw *responseWrapper) Write(data []byte) (int, error) {
	if !rw.headersSent {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(data)
	rw.bytesWritten += int64(n)
	return n, err
}

func (rw *responseWrapper) StatusCode() int {
	if rw.statusCode == 0 && rw.headersSent {
		return http.StatusOK
	}
	return rw.statusCode
}

// Flush implements http.Flusher so SSE responses work through middleware.
func (rw *responseWrapper) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// streamResponse copies the upstream response to w, flushing SSE streams.
func streamResponse(w http.ResponseWriter, resp *http.Response) error {
	copyHeaders(w, resp.Header)
	w.WriteHeader(resp.StatusCode)

	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		if flusher, ok := w.(http.Flusher); ok {
			buf := make([]byte, 32*1024)
			for {
				n, err := resp.Body.Read(buf)
				if n > 0 {
					if _, writeErr := w.Write(buf[:n]); writeErr != nil {
						return writeErr
					}
					flusher.Flush()
				}
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
			}
		}
	}
	bufPtr := bufferPool.Get().(*[]byte)
	defer bufferPool.Put(bufPtr)
	_, err := io.CopyBuffer(w, resp.Body, *bufPtr)
	return err
}

// copyHeaders copies headers from src to w, skipping any named in skip.
func copyHeaders(w http.ResponseWriter, src http.Header, skip ...string) {
	skipSet := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipSet[strings.ToLower(s)] = true
	}
	for key, values := range src {
		if skipSet[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

// capabilityFromPath maps a request path to a Capability.
func capabilityFromPath(path string) (Capability, error) {
	return requestproxy.CapabilityFromPath(path)
}

// proxyHandler is the HTTP handler factory for proxied API paths.
func proxyHandler(registry *ProviderRegistry, router *ModelRouter, cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(cfg.Timeouts.ProxyContext)*time.Second)
		defer cancel()

		r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024)
		rw := &responseWrapper{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("proxy panic: %v", recovered)
				if !rw.headersSent {
					writeOpenAIError(rw, http.StatusInternalServerError,
						newProviderError("", ProviderErrorTransport, http.StatusInternalServerError, false, "internal server error"))
				}
			}
		}()
		// net/http already executes handlers concurrently. Keeping provider work on
		// this goroutine ensures ResponseWriter is never used after ServeHTTP
		// returns and makes the request context the sole lifetime boundary.
		err := processProxyRequest(registry, router, cfg, rw, r, ctx)
		if err == nil && ctx.Err() != nil && !rw.headersSent {
			err = ctx.Err()
		}
		if err != nil && !rw.headersSent {
			status := providerErrorStatus(err)
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			writeOpenAIError(rw, status, err)
		}
	}
}

// attemptResponseWriter buffers an upstream attempt's status and any initial
// body bytes so the failover loop can discard a failed attempt without
// committing bytes to the real client.
//
// Commit protocol:
//   - WriteHeader records the status code but does NOT forward it yet.
//   - Write buffers data. The first write after a ≥2xx status auto-commits:
//     headers and any buffered bytes flow to the real writer, then the new bytes
//     follow. From that point all further writes/flushes pass through directly.
//   - CommitToReal forces the commit (used by the failover loop on success).
//   - If the attempt fails before any commit, the caller discards the
//     attemptResponseWriter and creates a fresh one for the next attempt.
type attemptResponseWriter struct {
	real            http.ResponseWriter
	statusCode      int
	headersSent     bool
	outputCommitted bool
	buf             bytes.Buffer
}

func newAttemptResponseWriter(real http.ResponseWriter) *attemptResponseWriter {
	return &attemptResponseWriter{real: real}
}

func (a *attemptResponseWriter) Header() http.Header { return a.real.Header() }

func (a *attemptResponseWriter) WriteHeader(code int) {
	if a.headersSent {
		return
	}
	a.headersSent = true
	a.statusCode = code
}

func (a *attemptResponseWriter) Write(data []byte) (int, error) {
	if a.outputCommitted {
		return a.real.Write(data)
	}
	// Auto-commit on the first write after a successful (non-error) status so
	// streaming responses are not buffered in their entirety.
	status := a.StatusCode()
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		_ = a.CommitToReal()
		return a.real.Write(data)
	}
	return a.buf.Write(data)
}

func (a *attemptResponseWriter) Flush() {
	if a.outputCommitted {
		if f, ok := a.real.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func (a *attemptResponseWriter) StatusCode() int {
	if a.statusCode == 0 {
		return http.StatusOK
	}
	return a.statusCode
}

// OutputCommitted reports whether any response bytes have been forwarded to the
// real writer (i.e. the response is no longer discardable).
func (a *attemptResponseWriter) OutputCommitted() bool { return a.outputCommitted }

// CommitToReal flushes buffered status/headers/body to the real writer and
// switches to pass-through mode. After this call all further writes go directly
// to the real writer without buffering. It is idempotent.
func (a *attemptResponseWriter) CommitToReal() error {
	if a.outputCommitted {
		return nil
	}
	a.outputCommitted = true
	a.real.WriteHeader(a.StatusCode())
	if a.buf.Len() > 0 {
		_, err := a.real.Write(a.buf.Bytes())
		a.buf.Reset()
		return err
	}
	return nil
}

// isEligibleFailoverStatus reports whether the HTTP status code from an
// upstream attempt qualifies the logical request for failover to the next
// target. Failover is only eligible before any response bytes reach the client.
func isEligibleFailoverStatus(status int, err error) bool {
	if err != nil {
		return true
	}
	switch status {
	case http.StatusTooManyRequests,
		http.StatusUnauthorized,
		http.StatusPaymentRequired,
		http.StatusForbidden,
		http.StatusRequestTimeout,
		http.StatusGatewayTimeout:
		return true
	}
	return status >= http.StatusInternalServerError
}

// processProxyRequest resolves a route and delegates to the selected provider.
// For umbrella (virtual) model requests it performs sequential failover: after
// an eligible pre-output failure it records a cooldown, resolves the next
// available target, and retries. All attempts share the original body and
// request context. The same response writer and telemetry handle are used
// throughout; only the route and provider change between attempts.
func processProxyRequest(
	_ *ProviderRegistry,
	router *ModelRouter,
	cfg *Config,
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
) error {
	_ = cfg
	reqStart := time.Now()
	capability, err := capabilityFromPath(r.URL.Path)
	if err != nil {
		return newProviderError("", ProviderErrorInvalidRequest, http.StatusNotFound, false, "%v", err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return newProviderError("", ProviderErrorInvalidRequest, http.StatusBadRequest, false, "reading request body: %v", err)
	}
	defer r.Body.Close()

	requestedModel := extractModelFromBody(body)
	log.Printf("[REQ] %s %s model=%q capability=%s body_size=%d from=%s",
		r.Method, r.URL.Path, requestedModel, capability, len(body), r.RemoteAddr)

	route, err := router.Resolve(ctx, requestedModel, capability)
	if err != nil {
		var noTarget *routingpkg.NoActiveTargetError
		if errors.As(err, &noTarget) {
			logUmbrellaNoTarget(requestedModel, noTarget)
			return newProviderError("", ProviderErrorModelUnavailable, http.StatusServiceUnavailable, false,
				"no active target is available for model %q", requestedModel)
		}
		log.Printf("[REQ] %s %s model=%q → routing failed: %v", r.Method, r.URL.Path, requestedModel, err)
		return newProviderError("", ProviderErrorInvalidRequest, http.StatusBadRequest, false, "routing: %v", err)
	}

	// Non-umbrella routes: single dispatch, no failover.
	if route.Selection == nil {
		return dispatchSingleRoute(route, router, r, body, capability, requestedModel, w, ctx, reqStart)
	}

	// Umbrella route: sequential failover loop. The set of attempted targets
	// grows with each attempt; the loop terminates when one attempt succeeds,
	// the context is cancelled, output is committed, or no more targets remain.
	attempted := make(map[string]struct{})
	var lastProxyErr error
	var lastStatus int
	attemptNum := 0

	// routeObserver is called once per attempt so the WS chat path can track
	// the current provider/model as failover progresses.
	observer := routeObserverFromContext(ctx)

	for {
		attemptNum++
		target := strings.TrimSpace(route.Selection.SelectedTarget)
		attempted[target] = struct{}{}

		if observer != nil {
			observer(route.Provider.ID(), route.ResolvedModel)
		}

		if route.Selection != nil {
			logUmbrellaSelection(route.Selection, route.Provider.ID())
		}

		attemptBody := body
		if route.ResolvedModel != requestedModel {
			log.Printf("[REQ] attempt %d model rewritten: %q → %q", attemptNum, requestedModel, route.ResolvedModel)
			attemptBody = patchBodyModel(body, route.ResolvedModel)
		}

		log.Printf("[REQ] attempt %d/%s %s %s model=%q → provider=%s upstream_model=%q",
			attemptNum, route.Selection.VirtualModel, r.Method, r.URL.Path,
			requestedModel, route.Provider.ID(), route.ResolvedModel)

		telemetryHandle := currentTelemetryRecorder().Begin(string(route.Provider.ID()), route.ResolvedModel)

		// Buffer this attempt behind an attempt writer so we can discard a
		// failed attempt without committing bytes to the real client.
		attemptWriter := newAttemptResponseWriter(w)
		virtualWriter := newVirtualModelResponseWriter(attemptWriter, requestedModel)
		usageWriter := newUsageSniffWriter(virtualWriter)

		proxyErr := route.Provider.ProxyRequest(ctx, usageWriter, r, attemptBody, capability)
		if commitErr := virtualWriter.Commit(); commitErr != nil && proxyErr == nil {
			proxyErr = commitErr
		}

		// Determine outcome status.
		status := responseStatus(virtualWriter)
		if proxyErr != nil && status == http.StatusOK {
			status = providerErrorStatus(proxyErr)
		}

		router.RecordOutcome(route.Selection, status, proxyErr, retryAfterDuration(attemptWriter.real.Header().Get("Retry-After")))

		telemetrySuccess := proxyErr == nil && status < http.StatusBadRequest
		telemetryHandle.Finish(telemetrypkg.Outcome{
			Success:         telemetrySuccess,
			ErrorCategory:   classifyErrorCategory(status, proxyErr),
			ProducedMessage: telemetrySuccess && capability != CapabilityEmbeddings,
			Usage:           usageWriter.Usage(),
		})

		// On success (or if output has already started flowing), commit and return.
		if telemetrySuccess || attemptWriter.OutputCommitted() {
			if telemetrySuccess {
				router.RecordPreferred(route.Selection.VirtualModel, capability, target)
			}
			if !attemptWriter.OutputCommitted() {
				if err := attemptWriter.CommitToReal(); err != nil {
					return err
				}
			}
			elapsed := time.Since(reqStart)
			log.Printf("[REQ] completed %s %s model=%q provider=%s status=%d attempt=%d elapsed=%s",
				r.Method, r.URL.Path, requestedModel, route.Provider.ID(), status, attemptNum, elapsed)
			return proxyErr
		}

		// Check failover eligibility.
		lastProxyErr = proxyErr
		lastStatus = status

		if !isEligibleFailoverStatus(status, proxyErr) {
			// Non-retriable failure — commit the error response and stop.
			_ = attemptWriter.CommitToReal()
			elapsed := time.Since(reqStart)
			log.Printf("[REQ] completed %s %s model=%q provider=%s status=%d attempt=%d elapsed=%s error=%v",
				r.Method, r.URL.Path, requestedModel, route.Provider.ID(), status, attemptNum, elapsed, proxyErr)
			return proxyErr
		}

		if ctx.Err() != nil {
			// Client cancelled or deadline exceeded — stop.
			log.Printf("[REQ] failover aborted after attempt %d: context %v", attemptNum, ctx.Err())
			return proxyErr
		}

		log.Printf("[REQ] attempt %d/%s failed (status=%d), trying next target",
			attemptNum, route.Selection.VirtualModel, status)

		// Try to get the next routable target.
		nextRoute, nextErr := router.ResolveNext(ctx, route.Selection.VirtualModel, capability, attempted)
		if nextErr != nil {
			// No more targets.
			var noTarget *routingpkg.NoActiveTargetError
			if errors.As(nextErr, &noTarget) {
				logUmbrellaNoTarget(requestedModel, noTarget)
			}
			elapsed := time.Since(reqStart)
			log.Printf("[REQ] all targets exhausted for %q after %d attempt(s) elapsed=%s",
				requestedModel, attemptNum, elapsed)
			return newProviderError("", ProviderErrorModelUnavailable, http.StatusServiceUnavailable, false,
				"all active targets failed for model %q after %d attempt(s)", requestedModel, attemptNum)
		}
		route = nextRoute
		_ = lastProxyErr
		_ = lastStatus
	}
}

// dispatchSingleRoute handles non-umbrella routes (explicit provider-prefix,
// model-map, catalog). These routes dispatch exactly once with no failover.
func dispatchSingleRoute(
	route *RouteResult,
	router *ModelRouter,
	r *http.Request,
	body []byte,
	capability Capability,
	requestedModel string,
	w http.ResponseWriter,
	ctx context.Context,
	reqStart time.Time,
) error {
	if observer := routeObserverFromContext(ctx); observer != nil {
		observer(route.Provider.ID(), route.ResolvedModel)
	}

	if route.ResolvedModel != requestedModel {
		log.Printf("[REQ] model rewritten: %q → %q", requestedModel, route.ResolvedModel)
		body = patchBodyModel(body, route.ResolvedModel)
	}

	log.Printf("[REQ] routing %s %s model=%q → provider=%s upstream_model=%q",
		r.Method, r.URL.Path, requestedModel, route.Provider.ID(), route.ResolvedModel)

	telemetryHandle := currentTelemetryRecorder().Begin(string(route.Provider.ID()), route.ResolvedModel)

	usageWriter := newUsageSniffWriter(w)
	proxyErr := route.Provider.ProxyRequest(ctx, usageWriter, r, body, capability)

	status := responseStatus(w)
	if proxyErr != nil && status == http.StatusOK {
		status = providerErrorStatus(proxyErr)
	}

	telemetrySuccess := proxyErr == nil && status < http.StatusBadRequest
	telemetryHandle.Finish(telemetrypkg.Outcome{
		Success:         telemetrySuccess,
		ErrorCategory:   classifyErrorCategory(status, proxyErr),
		ProducedMessage: telemetrySuccess && capability != CapabilityEmbeddings,
		Usage:           usageWriter.Usage(),
	})
	elapsed := time.Since(reqStart)
	if proxyErr != nil {
		log.Printf("[REQ] completed %s %s model=%q provider=%s status=%d elapsed=%s error=%v",
			r.Method, r.URL.Path, requestedModel, route.Provider.ID(), status, elapsed, proxyErr)
	} else {
		log.Printf("[REQ] completed %s %s model=%q provider=%s status=%d elapsed=%s",
			r.Method, r.URL.Path, requestedModel, route.Provider.ID(), status, elapsed)
	}
	return proxyErr
}

func retryAfterDuration(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		return 0
	}
	duration := time.Duration(seconds) * time.Second
	if duration > 5*time.Minute {
		return 5 * time.Minute
	}
	return duration
}
