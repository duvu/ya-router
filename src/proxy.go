package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"
)

const (
	copilotAPIBase = "https://api.githubcopilot.com"

	// Retry configuration for chat completions
	maxChatRetries     = 3
	baseChatRetryDelay = 1 // seconds

	// Circuit breaker configuration - timeout will be loaded from config
	circuitBreakerFailureThreshold = 5
)

// Simple circuit breaker state
type CircuitBreakerState int

const (
	CircuitClosed CircuitBreakerState = iota
	CircuitOpen
	CircuitHalfOpen
)

// Circuit breaker for upstream API calls
type CircuitBreaker struct {
	failureCount    int64
	lastFailureTime time.Time
	state           CircuitBreakerState
	timeout         time.Duration
	mutex           sync.RWMutex
}

var circuitBreaker = &CircuitBreaker{
	state:   CircuitClosed,
	timeout: 30 * time.Second, // Default, will be updated from config
}

// validateAndTransformRequestModel parses the request body, validates the model, and transforms it if needed
func validateAndTransformRequestModel(body []byte, cfg *Config) ([]byte, error) {
	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		// If we can't parse it, just pass it through
		log.Printf("Could not parse request JSON (passing through): %v", err)
		return body, nil
	}

	originalModel := req.Model
	validatedModel := validateAndTransformModel(req.Model, cfg)

	// If model was changed, log it and update the request
	if originalModel != validatedModel {
		log.Printf("Model transformed: %s -> %s", originalModel, validatedModel)
		req.Model = validatedModel

		// Re-marshal the request
		newBody, err := json.Marshal(req)
		if err != nil {
			log.Printf("Error marshaling transformed request: %v", err)
			return body, nil // Return original on error
		}
		return newBody, nil
	}

	// No change needed
	return body, nil
}

var tokenMu sync.Mutex

// Circuit breaker methods
func (cb *CircuitBreaker) canExecute() bool {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	if cb.state == CircuitClosed {
		return true
	}

	if cb.state == CircuitOpen {
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.mutex.RUnlock()
			cb.mutex.Lock()
			cb.state = CircuitHalfOpen
			cb.mutex.Unlock()
			cb.mutex.RLock()
			return true
		}
		return false
	}

	// CircuitHalfOpen
	return true
}

func (cb *CircuitBreaker) onSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.failureCount = 0
	cb.state = CircuitClosed
}

func (cb *CircuitBreaker) onFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.failureCount++
	cb.lastFailureTime = time.Now()

	if cb.failureCount >= circuitBreakerFailureThreshold {
		cb.state = CircuitOpen
	}
}

var sharedHTTPClient *http.Client

// initializeTimeouts initializes all timeout configurations from config
func initializeTimeouts(cfg *Config) {
	// Update circuit breaker timeout
	circuitBreaker.mutex.Lock()
	circuitBreaker.timeout = time.Duration(cfg.Timeouts.CircuitBreaker) * time.Second
	circuitBreaker.mutex.Unlock()

	// Initialize HTTP client with config timeouts
	sharedHTTPClient = &http.Client{
		Timeout: time.Duration(cfg.Timeouts.HTTPClient) * time.Second,
		Transport: &http.Transport{
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

// Buffer pool for request/response reuse
var bufferPool = sync.Pool{
	New: func() interface{} {
		// 32KB buffer for efficient copying
		b := make([]byte, 32*1024)
		return &b
	},
}

// Worker pool for handling requests
type WorkerPool struct {
	workers  int
	jobQueue chan func()
	quit     chan bool
	wg       sync.WaitGroup
}

func NewWorkerPool(workers int) *WorkerPool {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	wp := &WorkerPool{
		workers:  workers,
		jobQueue: make(chan func(), workers*2), // Buffer for burst traffic
		quit:     make(chan bool),
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
					job()
				case <-wp.quit:
					return
				}
			}
		}()
	}
}

func (wp *WorkerPool) Submit(job func()) {
	wp.jobQueue <- job
}

func (wp *WorkerPool) Stop() {
	close(wp.quit)
	wp.wg.Wait()
}

// Global worker pool
var globalWorkerPool = NewWorkerPool(runtime.NumCPU() * 2)

// Request coalescing for identical requests
type CoalescingCache struct {
	requests map[string]chan interface{}
	mutex    sync.RWMutex
}

func NewCoalescingCache() *CoalescingCache {
	return &CoalescingCache{
		requests: make(map[string]chan interface{}),
	}
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

	// Check if request is already in progress
	if ch, exists := cc.requests[key]; exists {
		cc.mutex.Unlock()
		// Wait for the existing request to complete
		return <-ch
	}

	// Create new channel for this request
	ch := make(chan interface{}, 1)
	cc.requests[key] = ch
	cc.mutex.Unlock()

	// Execute the request
	result := fn()

	// Broadcast result to all waiting goroutines
	ch <- result
	close(ch)

	// Clean up
	cc.mutex.Lock()
	delete(cc.requests, key)
	cc.mutex.Unlock()

	return result
}

// Global coalescing cache for models endpoint
var modelsCoalescingCache = NewCoalescingCache()

func ensureValidToken(cfg *Config) error {
	tokenMu.Lock()
	defer tokenMu.Unlock()

	now := time.Now().Unix()

	// Check if token is completely missing
	if cfg.CopilotToken == "" {
		log.Printf("No Copilot token found, starting authentication")
		return authenticate(cfg)
	}

	// Proactive refresh: refresh when 20% of lifetime remains or <5 minutes
	timeUntilExpiry := cfg.ExpiresAt - now
	refreshThreshold := int64(300) // 5 minutes
	if cfg.RefreshIn > 0 {
		// Use 20% of RefreshIn as threshold, but minimum 5 minutes
		proactiveThreshold := cfg.RefreshIn / 5 // 20% = 1/5
		if proactiveThreshold > refreshThreshold {
			refreshThreshold = proactiveThreshold
		}
	}

	if timeUntilExpiry <= refreshThreshold {
		log.Printf("Token expires in %d seconds (threshold: %d), attempting refresh", timeUntilExpiry, refreshThreshold)
		if err := refreshToken(cfg); err != nil {
			log.Printf("Token refresh failed, falling back to full authentication: %v", err)
			return authenticate(cfg)
		}
		log.Printf("Token refresh completed successfully")
	} else {
		log.Printf("Token is valid: expires in %d seconds", timeUntilExpiry)
	}

	return nil
}

// isRetriableError determines if an HTTP error should be retried
func isRetriableError(statusCode int, err error) bool {
	if err != nil {
		return true // Network errors are retriable
	}

	// Retry on server errors and rate limiting
	return statusCode >= 500 || statusCode == 429 || statusCode == 408
}

// makeRequestWithRetry performs HTTP request with exponential backoff retry
func makeRequestWithRetry(client *http.Client, req *http.Request, body []byte) (*http.Response, error) {
	var lastResp *http.Response
	var lastErr error

	for attempt := 1; attempt <= maxChatRetries; attempt++ {
		// Create a new request for each attempt (in case body was consumed)
		retryReq, err := http.NewRequest(req.Method, req.URL.String(), bytes.NewBuffer(body))
		if err != nil {
			return nil, err
		}

		// Copy all headers
		for key, values := range req.Header {
			for _, value := range values {
				retryReq.Header.Add(key, value)
			}
		}

		log.Printf("Chat completion attempt %d/%d", attempt, maxChatRetries)

		resp, err := client.Do(retryReq)
		if err != nil {
			lastErr = err
			if attempt == maxChatRetries {
				log.Printf("Request failed after %d attempts: %v", maxChatRetries, err)
				return nil, err
			}

			waitTime := time.Duration(baseChatRetryDelay*attempt*attempt) * time.Second
			log.Printf("Request failed (attempt %d), retrying in %v: %v", attempt, waitTime, err)
			time.Sleep(waitTime)
			continue
		}

		lastResp = resp

		// Check if we should retry based on status code
		if !isRetriableError(resp.StatusCode, nil) {
			log.Printf("Request successful on attempt %d: %d", attempt, resp.StatusCode)
			return resp, nil
		}

		// Close the response body before retrying
		resp.Body.Close()

		if attempt == maxChatRetries {
			log.Printf("Request failed after %d attempts with status: %d", maxChatRetries, resp.StatusCode)
			return resp, nil // Return the last response even if it failed
		}

		waitTime := time.Duration(baseChatRetryDelay*attempt*attempt) * time.Second
		log.Printf("Request failed with status %d (attempt %d), retrying in %v", resp.StatusCode, attempt, waitTime)
		time.Sleep(waitTime)
	}

	return lastResp, lastErr
}

func proxyHandler(cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Create context with extended timeout for long-lived streaming responses
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(cfg.Timeouts.ProxyContext)*time.Second)
		defer cancel()

		// Check circuit breaker
		if !circuitBreaker.canExecute() {
			log.Printf("Circuit breaker is open, rejecting request")
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
			return
		}

		// Limit request body size to 5MB
		r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024)

		// For streaming responses, we need to handle them differently
		// Use a response wrapper to track if headers have been sent
		respWrapper := &responseWrapper{ResponseWriter: w, headersSent: false}

		// Create a done channel to track completion
		done := make(chan error, 1)

		// Submit request to worker pool
		globalWorkerPool.Submit(func() {
			defer func() {
				if recovery := recover(); recovery != nil {
					log.Printf("Worker panic recovered: %v", recovery)
					done <- fmt.Errorf("internal server error")
				}
			}()

			err := processProxyRequest(cfg, respWrapper, r, ctx)
			done <- err
		})

		// Wait for worker to complete or context timeout
		select {
		case err := <-done:
			if err != nil {
				log.Printf("Worker error: %v", err)
				// Only write error if headers haven't been sent
				if !respWrapper.headersSent {
					http.Error(w, err.Error(), http.StatusInternalServerError)
				}
			}
		case <-ctx.Done():
			log.Printf("Request timeout in worker pool")
			// Only write timeout error if headers haven't been sent
			if !respWrapper.headersSent {
				http.Error(w, "Request timeout", http.StatusRequestTimeout)
			}
		}
	}
}

// Response wrapper to track if headers have been sent
type responseWrapper struct {
	http.ResponseWriter
	headersSent bool
}

func (rw *responseWrapper) WriteHeader(statusCode int) {
	if !rw.headersSent {
		rw.headersSent = true
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

func (rw *responseWrapper) Write(data []byte) (int, error) {
	if !rw.headersSent {
		rw.headersSent = true
	}
	return rw.ResponseWriter.Write(data)
}

// Process proxy request in worker goroutine - returns error instead of using channel
func processProxyRequest(cfg *Config, w http.ResponseWriter, r *http.Request, ctx context.Context) error {
	log.Printf("Starting processProxyRequest for %s %s", r.Method, r.URL.Path)

	if err := ensureValidToken(cfg); err != nil {
		log.Printf("Token validation failed: %v", err)
		return fmt.Errorf("authentication required")
	}

	// Log request
	log.Printf("Request: %s %s", r.Method, r.URL.Path)
	log.Printf("Request Content-Length: %d", r.ContentLength)

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		return fmt.Errorf("error reading request")
	}
	defer r.Body.Close()

	// Transform and validate model if this is a chat completion request
	if r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/chat/completions/" {
		body, err = validateAndTransformRequestModel(body, cfg)
		if err != nil {
			log.Printf("Error validating request model: %v", err)
			return fmt.Errorf("error processing request")
		}
	}

	// Transform path
	targetPath := "/chat/completions"
	if r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/chat/completions/" {
		targetPath = "/chat/completions"
	}

	// Create new request to GitHub Copilot with context
	targetURL := copilotAPIBase + targetPath
	log.Printf("Sending to: %s", targetURL)
	log.Printf("Request body length: %d", len(body))

	req, err := http.NewRequestWithContext(ctx, r.Method, targetURL, bytes.NewBuffer(body))
	if err != nil {
		log.Printf("Error creating request: %v", err)
		return fmt.Errorf("error creating request")
	}

	// Set headers exactly as the working direct approach
	req.Header.Set("Authorization", "Bearer "+cfg.CopilotToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.26.7")
	req.Header.Set("Editor-Version", "vscode/1.99.3")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
	req.Header.Set("Copilot-Integration-Id", "vscode-chat")
	req.Header.Set("Openai-Intent", "conversation-edits")
	req.Header.Set("X-Initiator", "user")

	// Make the request with retry logic using shared client
	resp, err := makeRequestWithRetry(sharedHTTPClient, req, body)
	if err != nil {
		circuitBreaker.onFailure()
		log.Printf("Error making request after retries: %v", err)
		return fmt.Errorf("error making request")
	}
	defer resp.Body.Close()

	// Success - notify circuit breaker
	if resp.StatusCode < 500 {
		circuitBreaker.onSuccess()
	} else {
		circuitBreaker.onFailure()
	}

	log.Printf("Response: %d - Content-Type: %s", resp.StatusCode, resp.Header.Get("Content-Type"))

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Add CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")

	// Copy status code
	w.WriteHeader(resp.StatusCode)

	// For streaming responses, use direct copy without buffer pooling
	// to avoid blocking the stream
	if resp.Header.Get("Content-Type") == "text/event-stream" {
		log.Printf("Starting streaming response copy")
		// Stream directly for event-stream responses with flushing support
		if flusher, ok := w.(http.Flusher); ok {
			// Copy in chunks and flush periodically for better streaming
			buf := make([]byte, 1024) // Small buffer for streaming
			for {
				n, err := resp.Body.Read(buf)
				if n > 0 {
					_, writeErr := w.Write(buf[:n])
					if writeErr != nil {
						log.Printf("Error writing streaming chunk: %v", writeErr)
						return writeErr
					}
					flusher.Flush() // Flush immediately for streaming
				}
				if err == io.EOF {
					log.Printf("Streaming response completed successfully")
					break
				}
				if err != nil {
					log.Printf("Error reading streaming response: %v", err)
					return err
				}
			}
		} else {
			// Fallback to direct copy if no flusher available
			_, err = io.Copy(w, resp.Body)
		}
	} else {
		log.Printf("Starting regular response copy")
		// Use buffer pool for regular responses
		bufPtr := bufferPool.Get().(*[]byte)
		defer bufferPool.Put(bufPtr)
		buf := *bufPtr
		_, err = io.CopyBuffer(w, resp.Body, buf)
	}

	if err != nil {
		log.Printf("Error copying response: %v", err)
		return err
	}

	// Signal successful completion
	log.Printf("processProxyRequest completed successfully")
	return nil
}
