#!/usr/bin/env python3
"""Apply the routing/auth/transport hardening change to the Go runtime.

This script is intentionally strict and idempotent. It is executed by a temporary
GitHub Actions workflow so the repository can patch and validate itself without
requiring local checkout access from the automation client.
"""

from __future__ import annotations

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def read(path: str) -> str:
    return (ROOT / path).read_text(encoding="utf-8")


def write(path: str, content: str) -> None:
    target = ROOT / path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(content, encoding="utf-8")


def replace_once(text: str, old: str, new: str, label: str) -> str:
    if old in text:
        return text.replace(old, new, 1)
    if new in text:
        return text
    raise RuntimeError(f"patch target not found: {label}")


def replace_between(text: str, start: str, end: str | None, replacement: str, label: str) -> str:
    start_idx = text.find(start)
    if start_idx < 0:
        if replacement.strip() in text:
            return text
        raise RuntimeError(f"start marker not found: {label}: {start!r}")
    if end is None:
        end_idx = len(text)
    else:
        end_idx = text.find(end, start_idx)
        if end_idx < 0:
            raise RuntimeError(f"end marker not found: {label}: {end!r}")
    return text[:start_idx] + replacement + text[end_idx:]


def regex_replace_once(text: str, pattern: str, replacement: str, label: str) -> str:
    new, count = re.subn(pattern, replacement, text, count=1, flags=re.DOTALL)
    if count == 1:
        return new
    if replacement.strip() in text:
        return text
    raise RuntimeError(f"regex target not found: {label}")


def patch_provider() -> None:
    path = "src/provider.go"
    text = read(path)
    text = replace_once(
        text,
        'const (\n\tCapabilityChat       Capability = "chat"\n\tCapabilityEmbeddings Capability = "embeddings"\n)',
        'const (\n\tCapabilityChat       Capability = "chat"\n\tCapabilityResponses  Capability = "responses"\n\tCapabilityEmbeddings Capability = "embeddings"\n)',
        "provider capabilities",
    )
    write(path, text)

    write(
        "src/provider_error.go",
        '''package main

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
)

type ProviderErrorKind string

const (
    ProviderErrorInvalidRequest ProviderErrorKind = "invalid_request"
    ProviderErrorAuthRequired   ProviderErrorKind = "auth_required"
    ProviderErrorEntitlement    ProviderErrorKind = "entitlement_denied"
    ProviderErrorRateLimit      ProviderErrorKind = "rate_limited"
    ProviderErrorUnsupported    ProviderErrorKind = "unsupported_capability"
    ProviderErrorUnavailable    ProviderErrorKind = "provider_unavailable"
    ProviderErrorTransport      ProviderErrorKind = "transport_error"
)

type ProviderError struct {
    Kind       ProviderErrorKind
    StatusCode int
    Retryable  bool
    Provider   ProviderID
    Err        error
}

func (e *ProviderError) Error() string {
    if e == nil {
        return "provider error"
    }
    if e.Err != nil {
        return e.Err.Error()
    }
    return string(e.Kind)
}

func (e *ProviderError) Unwrap() error { return e.Err }

func newProviderError(provider ProviderID, kind ProviderErrorKind, status int, retryable bool, format string, args ...interface{}) error {
    return &ProviderError{
        Kind:       kind,
        StatusCode: status,
        Retryable:  retryable,
        Provider:   provider,
        Err:        fmt.Errorf(format, args...),
    }
}

func providerErrorStatus(err error) int {
    if err == nil {
        return http.StatusOK
    }
    var providerErr *ProviderError
    if errors.As(err, &providerErr) && providerErr.StatusCode > 0 {
        return providerErr.StatusCode
    }
    if errors.Is(err, context.DeadlineExceeded) {
        return http.StatusGatewayTimeout
    }
    if errors.Is(err, context.Canceled) {
        return http.StatusRequestTimeout
    }
    return http.StatusBadGateway
}

func writeOpenAIError(w http.ResponseWriter, status int, err error) {
    if status <= 0 {
        status = http.StatusInternalServerError
    }
    message := "request failed"
    errorType := "proxy_error"
    var providerErr *ProviderError
    if err != nil {
        message = err.Error()
    }
    if errors.As(err, &providerErr) {
        errorType = string(providerErr.Kind)
    }
    payload := map[string]interface{}{
        "error": map[string]interface{}{
            "message": message,
            "type":    errorType,
        },
    }
    body, _ := json.Marshal(payload)
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _, _ = w.Write(body)
}

func responseStatus(w http.ResponseWriter) int {
    type statusWriter interface{ StatusCode() int }
    if sw, ok := w.(statusWriter); ok {
        if status := sw.StatusCode(); status > 0 {
            return status
        }
    }
    return http.StatusOK
}
''',
    )


def patch_proxy() -> None:
    path = "src/proxy.go"
    text = read(path)

    retry_impl = '''// makeRequestWithRetry executes a request with bounded retries.
// Unsafe methods are retried only when the caller supplied an Idempotency-Key;
// this prevents duplicate model generations after uncertain delivery.
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
            log.Printf("Upstream attempt %d/%d FAILED after %s: %v", attempt, maxChatRetries, elapsed, err)
            lastErr = err
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

'''
    text = replace_between(text, "// makeRequestWithRetry executes", "// responseWrapper tracks", retry_impl, "safe retry")

    wrapper_impl = '''// responseWrapper tracks the committed status and response size.
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

'''
    text = replace_between(text, "// responseWrapper tracks", "// Flush implements", wrapper_impl, "response status tracking")
    text = text.replace('\tw.Header().Set("Access-Control-Allow-Origin", "*")\n', "")
    text = text.replace('\tw.Header().Set("Access-Control-Allow-Headers", "*")\n', "")

    capability_impl = '''// capabilityFromPath maps a request path to a Capability.
func capabilityFromPath(path string) (Capability, error) {
    switch {
    case strings.Contains(path, "/chat/completions"):
        return CapabilityChat, nil
    case strings.Contains(path, "/responses"):
        return CapabilityResponses, nil
    case strings.Contains(path, "/embeddings"):
        return CapabilityEmbeddings, nil
    default:
        return "", fmt.Errorf("unsupported path: %s", path)
    }
}

'''
    text = replace_between(text, "// capabilityFromPath maps", "// proxyHandler is", capability_impl, "responses capability")

    handler_impl = '''// proxyHandler is the HTTP handler factory for proxied API paths.
func proxyHandler(registry *ProviderRegistry, router *ModelRouter, cfg *Config) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx, cancel := context.WithTimeout(r.Context(), time.Duration(cfg.Timeouts.ProxyContext)*time.Second)
        defer cancel()

        r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024)
        rw := &responseWrapper{ResponseWriter: w}
        done := make(chan error, 1)

        globalWorkerPool.Submit(func() {
            defer func() {
                if rec := recover(); rec != nil {
                    log.Printf("Worker panic: %v", rec)
                    done <- newProviderError("", ProviderErrorTransport, http.StatusInternalServerError, false, "internal server error")
                }
            }()
            done <- processProxyRequest(registry, router, cfg, rw, r, ctx)
        })

        select {
        case err := <-done:
            if err != nil && !rw.headersSent {
                writeOpenAIError(rw, providerErrorStatus(err), err)
            }
        case <-ctx.Done():
            if !rw.headersSent {
                writeOpenAIError(rw, http.StatusGatewayTimeout, ctx.Err())
            }
        }
    }
}

'''
    text = replace_between(text, "// proxyHandler is", "// processProxyRequest resolves", handler_impl, "proxy error handling")

    process_impl = '''// processProxyRequest resolves the route before applying provider-specific optimizations.
func processProxyRequest(
    registry *ProviderRegistry,
    router *ModelRouter,
    cfg *Config,
    w http.ResponseWriter,
    r *http.Request,
    ctx context.Context,
) error {
    reqStart := time.Now()
    cap, err := capabilityFromPath(r.URL.Path)
    if err != nil {
        log.Printf("[REQ] %s %s → unsupported path", r.Method, r.URL.Path)
        return newProviderError("", ProviderErrorInvalidRequest, http.StatusNotFound, false, "%v", err)
    }

    body, err := io.ReadAll(r.Body)
    if err != nil {
        return newProviderError("", ProviderErrorInvalidRequest, http.StatusBadRequest, false, "reading request body: %v", err)
    }
    defer r.Body.Close()

    requestedModel := extractModelFromBody(body)
    log.Printf("[REQ] %s %s model=%q capability=%s body_size=%d from=%s",
        r.Method, r.URL.Path, requestedModel, cap, len(body), r.RemoteAddr)

    route, err := router.Resolve(ctx, requestedModel, cap)
    if err != nil {
        log.Printf("[REQ] %s %s model=%q → routing FAILED: %v", r.Method, r.URL.Path, requestedModel, err)
        return newProviderError("", ProviderErrorInvalidRequest, http.StatusBadRequest, false, "routing: %v", err)
    }

    if route.ResolvedModel != requestedModel {
        log.Printf("[REQ] model rewritten: %q → %q", requestedModel, route.ResolvedModel)
        body = patchBodyModel(body, route.ResolvedModel)
    }

    log.Printf("[REQ] Routing %s %s model=%q → provider=%s upstream_model=%q",
        r.Method, r.URL.Path, requestedModel, route.Provider.ID(), route.ResolvedModel)

    var proxyErr error
    if cap == CapabilityChat && route.Provider.ID() == ProviderCopilot {
        if freeChatProvider, ok := route.Provider.(FreeChatProxyProvider); ok {
            proxyErr = freeChatProvider.ProxyFreeChatRequest(ctx, w, r, body, requestedModel)
        } else {
            proxyErr = route.Provider.ProxyRequest(ctx, w, r, body, cap)
        }
    } else {
        proxyErr = route.Provider.ProxyRequest(ctx, w, r, body, cap)
    }

    elapsed := time.Since(reqStart)
    status := responseStatus(w)
    if proxyErr != nil {
        log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s status=%d elapsed=%s ERROR: %v",
            r.Method, r.URL.Path, requestedModel, route.Provider.ID(), status, elapsed, proxyErr)
    } else {
        log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s status=%d elapsed=%s OK",
            r.Method, r.URL.Path, requestedModel, route.Provider.ID(), status, elapsed)
    }
    return proxyErr
}
'''
    text = replace_between(text, "// processProxyRequest resolves", None, process_impl, "route before free chat")
    write(path, text)


def patch_security_and_cli() -> None:
    write(
        "src/security.go",
        '''package main

import (
    "crypto/subtle"
    "fmt"
    "net"
    "net/http"
    "os"
    "strconv"
    "strings"
)

const (
    listenAddressEnv = "YA_ROUTER_LISTEN_ADDRESS"
    inboundAPIKeyEnv = "YA_ROUTER_API_KEY"
    corsOriginsEnv   = "YA_ROUTER_CORS_ALLOWED_ORIGINS"
)

func configuredListenAddress(port int) (string, error) {
    host := strings.TrimSpace(os.Getenv(listenAddressEnv))
    if host == "" {
        host = "127.0.0.1"
    }
    hostForCheck := strings.Trim(host, "[]")
    loopback := strings.EqualFold(hostForCheck, "localhost")
    if ip := net.ParseIP(hostForCheck); ip != nil {
        loopback = ip.IsLoopback()
    }
    if !loopback && strings.TrimSpace(os.Getenv(inboundAPIKeyEnv)) == "" {
        return "", fmt.Errorf("%s must be set when %s is non-loopback", inboundAPIKeyEnv, listenAddressEnv)
    }
    return net.JoinHostPort(hostForCheck, strconv.Itoa(port)), nil
}

func secureHandler(next http.Handler) http.Handler {
    apiKey := strings.TrimSpace(os.Getenv(inboundAPIKeyEnv))
    allowedOrigins := make(map[string]struct{})
    for _, origin := range strings.Split(os.Getenv(corsOriginsEnv), ",") {
        if normalized := strings.TrimSpace(origin); normalized != "" {
            allowedOrigins[normalized] = struct{}{}
        }
    }

    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if origin := r.Header.Get("Origin"); origin != "" {
            if _, ok := allowedOrigins[origin]; ok {
                w.Header().Set("Access-Control-Allow-Origin", origin)
                w.Header().Set("Vary", "Origin")
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-API-Key")
                w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
            }
        }
        if r.Method == http.MethodOptions {
            w.WriteHeader(http.StatusNoContent)
            return
        }
        if strings.HasPrefix(r.URL.Path, "/health") {
            next.ServeHTTP(w, r)
            return
        }
        if apiKey != "" && !validInboundCredential(r, apiKey) {
            writeOpenAIError(w, http.StatusUnauthorized, newProviderError("", ProviderErrorAuthRequired, http.StatusUnauthorized, false, "invalid or missing proxy credential"))
            return
        }
        next.ServeHTTP(w, r)
    })
}

func validInboundCredential(r *http.Request, expected string) bool {
    supplied := strings.TrimSpace(r.Header.Get("X-API-Key"))
    if supplied == "" {
        authorization := strings.TrimSpace(r.Header.Get("Authorization"))
        if len(authorization) > len("Bearer ") && strings.EqualFold(authorization[:len("Bearer ")], "Bearer ") {
            supplied = strings.TrimSpace(authorization[len("Bearer "):])
        }
    }
    if len(supplied) != len(expected) {
        return false
    }
    return subtle.ConstantTimeCompare([]byte(supplied), []byte(expected)) == 1
}
''',
    )

    path = "src/cli.go"
    text = read(path)

    auth_impl = '''// handleAuthCodex runs the native OpenAI device-code flow and stores
// ChatGPT-backed credentials in ya-router's permission-restricted config.
// The official Codex credential store is treated as read-only import data.
func handleAuthCodex(accountLabel string) error {
    cfg, err := loadConfig()
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }
    initializeTimeouts(cfg)
    cfg.Providers.Codex.Enabled = true

    authPtr, label, resolveErr := resolveOrCreateCodexAccount(cfg, accountLabel)
    if resolveErr != nil {
        return resolveErr
    }
    auth := &CodexAuthState{Mode: "chatgpt"}
    fmt.Printf("Starting OpenAI Codex authentication (account: %s, mode: chatgpt device_code)...\n", label)
    if err := codexAuthenticate(auth, func() error { return nil }); err != nil {
        return fmt.Errorf("Codex authentication failed: %w", err)
    }

    *authPtr = *auth
    clearPersistedChatGPTSecrets(&cfg.Providers.Codex.Auth)
    if err := saveConfig(cfg); err != nil {
        return fmt.Errorf("failed to persist Codex credentials: %w", err)
    }
    fmt.Println("Codex credentials stored in ya-router's local config (0600).")
    return nil
}

'''
    text = replace_between(text, "// handleAuthCodex runs", "func resolveOrCreateCodexAccount", auth_impl, "stop official store mutation")

    manual_impl = '''// handleAuthCodexManualToken sets a manually-provided access token.
// Prefer device authentication; manual tokens cannot be refreshed without a refresh token.
func handleAuthCodexManualToken(token, accountLabel string) error {
    cfg, err := loadConfig()
    if err != nil {
        return fmt.Errorf("failed to load config: %w", err)
    }
    initializeTimeouts(cfg)
    cfg.Providers.Codex.Enabled = true
    authPtr, _, resolveErr := resolveOrCreateCodexAccount(cfg, accountLabel)
    if resolveErr != nil {
        return resolveErr
    }
    authPtr.Mode = "chatgpt"
    authPtr.AccessToken = token
    authPtr.RefreshToken = ""
    authPtr.AccountID = extractAccountIDFromJWT(token)
    authPtr.ExpiresAt = extractJWTExpiry(token)
    if authPtr.ExpiresAt == 0 {
        authPtr.ExpiresAt = time.Now().Unix() + 3600
    }
    clearPersistedChatGPTSecrets(&cfg.Providers.Codex.Auth)
    if err := saveConfig(cfg); err != nil {
        return fmt.Errorf("failed to save config: %w", err)
    }
    fmt.Println("Codex access token stored in ya-router's local config.")
    fmt.Println("Manual tokens are not refreshable; use device authentication for normal operation.")
    return nil
}

'''
    text = replace_between(text, "// handleAuthCodexManualToken", "// handleStatus", manual_impl, "manual token store")

    server_old = '''\tmux := http.NewServeMux()
\tmux.HandleFunc("/v1/models", modelsHandler(registry, cfg))
\tmux.HandleFunc("/v1/models/", modelsHandler(registry, cfg))
\tmux.HandleFunc("/v1/embeddings", proxyHandler(registry, router, cfg))
\tmux.HandleFunc("/v1/embeddings/", proxyHandler(registry, router, cfg))
\tmux.HandleFunc("/v1/chat/completions", proxyHandler(registry, router, cfg))
\tmux.HandleFunc("/v1/chat/completions/", proxyHandler(registry, router, cfg))
\tmux.HandleFunc("/health", healthHandler)
\tmux.HandleFunc("/health/", healthHandler)
'''
    server_new = '''\tmux := http.NewServeMux()
\tmux.HandleFunc("/v1/models", modelsHandler(registry, cfg))
\tmux.HandleFunc("/v1/models/", modelsHandler(registry, cfg))
\tmux.HandleFunc("/v1/embeddings", proxyHandler(registry, router, cfg))
\tmux.HandleFunc("/v1/embeddings/", proxyHandler(registry, router, cfg))
\tmux.HandleFunc("/v1/chat/completions", proxyHandler(registry, router, cfg))
\tmux.HandleFunc("/v1/chat/completions/", proxyHandler(registry, router, cfg))
\tmux.HandleFunc("/v1/responses", proxyHandler(registry, router, cfg))
\tmux.HandleFunc("/v1/responses/", proxyHandler(registry, router, cfg))
\tmux.HandleFunc("/health", healthHandler)
\tmux.HandleFunc("/health/", healthHandler)
'''
    text = replace_once(text, server_old, server_new, "responses route")

    server_struct_old = '''\tserver := &http.Server{
\t\tAddr:         fmt.Sprintf(":%d", port),
\t\tHandler:      mux,
\t\tReadTimeout:  time.Duration(cfg.Timeouts.ServerRead) * time.Second,
\t\tWriteTimeout: time.Duration(cfg.Timeouts.ServerWrite) * time.Second,
\t\tIdleTimeout:  time.Duration(cfg.Timeouts.ServerIdle) * time.Second,
\t}
\tsetupGracefulShutdown(server)

\tfmt.Printf("Starting proxy on :%d\\n", port)
\tfmt.Printf("  /v1/models              → aggregated from all providers\\n")
\tfmt.Printf("  /v1/chat/completions    → routed per model\\n")
\tfmt.Printf("  /v1/embeddings          → routed per model\\n")
'''
    server_struct_new = '''\taddr, err := configuredListenAddress(port)
\tif err != nil {
\t\treturn err
\t}
\tserver := &http.Server{
\t\tAddr:         addr,
\t\tHandler:      secureHandler(mux),
\t\tReadTimeout:  time.Duration(cfg.Timeouts.ServerRead) * time.Second,
\t\tWriteTimeout: time.Duration(cfg.Timeouts.ServerWrite) * time.Second,
\t\tIdleTimeout:  time.Duration(cfg.Timeouts.ServerIdle) * time.Second,
\t}
\tsetupGracefulShutdown(server)

\tfmt.Printf("Starting proxy on %s\\n", addr)
\tfmt.Printf("  /v1/models              → aggregated from all providers\\n")
\tfmt.Printf("  /v1/chat/completions    → routed per model\\n")
\tfmt.Printf("  /v1/responses           → native Responses API\\n")
\tfmt.Printf("  /v1/embeddings          → routed per model\\n")
'''
    text = replace_once(text, server_struct_old, server_struct_new, "secure listen")

    # Runtime refresh must not write or prefer the single global official store.
    text = regex_replace_once(
        text,
        r'\n\t\t\tif err := persistToOfficialStore\(&working\); err != nil \{.*?\n\t\t\t\}',
        '',
        "pool refresh official store",
    )
    text = regex_replace_once(
        text,
        r'\n\tif err := persistToOfficialStore\(&working\); err != nil \{.*?\n\t\}',
        '',
        "single refresh official store",
    )
    text = text.replace(
        "\t\t\tresolved, err := resolveCodexChatGPTAuth(auth)\n",
        "\t\t\tvar resolved *resolvedCodexChatGPTAuth\n\t\t\tvar err error\n\t\t\tif auth.AccessToken == \"\" {\n\t\t\t\tresolved, err = resolveCodexChatGPTAuth(auth)\n\t\t\t}\n",
    )
    text = text.replace(
        "\tresolved, err := resolveCodexChatGPTAuth(auth)\n",
        "\tvar resolved *resolvedCodexChatGPTAuth\n\tvar err error\n\tif auth.AccessToken == \"\" {\n\t\tresolved, err = resolveCodexChatGPTAuth(auth)\n\t}\n",
        1,
    )
    write(path, text)


def patch_config_and_auth() -> None:
    path = "src/config.go"
    text = read(path)
    text = replace_once(
        text,
        '\tAPIKey       string `json:"api_key,omitempty"`\n',
        '\tAPIKey       string `json:"api_key,omitempty"`\n\tAPIKeyEnv    string `json:"api_key_env,omitempty"`\n',
        "api key env field",
    )
    text = replace_once(
        text,
        '\tcfg.Providers.Codex.Auth.Mode = "device_code"\n',
        '\tcfg.Providers.Codex.Auth.Mode = "device_code"\n\tcfg.Providers.Codex.Auth.APIKeyEnv = "OPENAI_API_KEY"\n',
        "api key env default",
    )
    text = replace_once(
        text,
        '\tif cfg.Providers.Codex.Auth.Mode == "" {\n\t\tcfg.Providers.Codex.Auth.Mode = "device_code"\n\t}\n',
        '\tif cfg.Providers.Codex.Auth.Mode == "" {\n\t\tcfg.Providers.Codex.Auth.Mode = "device_code"\n\t}\n\tif cfg.Providers.Codex.Auth.APIKeyEnv == "" {\n\t\tcfg.Providers.Codex.Auth.APIKeyEnv = "OPENAI_API_KEY"\n\t}\n',
        "loaded api key env default",
    )
    write(path, text)

    path = "src/codex_auth.go"
    text = read(path)
    text = text.replace(
        "// For chatgpt mode, tokens are read from / written to the official Codex\n// auth store (~/.codex/auth.json or $CODEX_HOME/auth.json).",
        "// The official Codex auth store is read-only import data. ya-router stores\n// its own runtime credentials in its permission-restricted configuration.",
    )

    # API-key resolution honors the configured environment variable.
    api_key_impl = '''func resolveCodexAPIKey(auth *CodexAuthState) (string, string, error) {
    envName := "OPENAI_API_KEY"
    if auth != nil && strings.TrimSpace(auth.APIKeyEnv) != "" {
        envName = strings.TrimSpace(auth.APIKeyEnv)
    }
    if key := strings.TrimSpace(os.Getenv(envName)); key != "" {
        return key, envName + " environment variable", nil
    }
    if auth != nil && strings.TrimSpace(auth.APIKey) != "" {
        return auth.APIKey, codexCredentialSourceProxyConfig, nil
    }
    official, err := loadOfficialCodexAuth()
    if err != nil {
        return "", "", err
    }
    if official != nil && strings.TrimSpace(official.APIKey) != "" {
        return official.APIKey, codexCredentialSourceOfficialStore, nil
    }
    return "", "", nil
}

'''
    text = replace_between(text, "func resolveCodexAPIKey(", "func resolveCodexChatGPTAuth(", api_key_impl, "api key env resolution")

    refresh_impl = '''// codexRefreshToken obtains a fresh access token. OAuth client errors are
// terminal; only transient network, 429, and 5xx failures are retried.
func codexRefreshToken(auth *CodexAuthState, save func() error) error {
    if auth.RefreshToken == "" {
        return errors.New("no refresh token available for Codex")
    }
    for attempt := 1; attempt <= maxRefreshRetries; attempt++ {
        form := url.Values{
            "client_id":     {codexOAuthClientID},
            "grant_type":    {"refresh_token"},
            "refresh_token": {auth.RefreshToken},
        }
        req, err := http.NewRequest("POST", codexAuthIssuer+"/oauth/token", strings.NewReader(form.Encode()))
        if err != nil {
            return err
        }
        req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
        req.Header.Set("User-Agent", codexUserAgent)

        resp, err := sharedHTTPClient.Do(req)
        if err != nil {
            if attempt == maxRefreshRetries {
                return fmt.Errorf("refresh failed after %d attempts: %w", maxRefreshRetries, err)
            }
            time.Sleep(time.Duration(baseRetryDelay*attempt*attempt) * time.Second)
            continue
        }
        body, readErr := io.ReadAll(resp.Body)
        resp.Body.Close()
        if readErr != nil {
            return readErr
        }
        if resp.StatusCode != http.StatusOK {
            retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
            if !retryable || attempt == maxRefreshRetries {
                return fmt.Errorf("refresh error (status %d): %s", resp.StatusCode, redactAuthError(body))
            }
            time.Sleep(time.Duration(baseRetryDelay*attempt*attempt) * time.Second)
            continue
        }

        var rr codexRefreshResp
        if err := json.Unmarshal(body, &rr); err != nil {
            return err
        }
        if rr.AccessToken == "" {
            return errors.New("refresh response did not include an access token")
        }
        auth.AccessToken = rr.AccessToken
        if rr.RefreshToken != "" {
            auth.RefreshToken = rr.RefreshToken
        }
        if rr.IDToken != "" {
            if accountID := extractAccountIDFromJWT(rr.IDToken); accountID != "" {
                auth.AccountID = accountID
            }
        }
        if auth.AccountID == "" {
            auth.AccountID = extractAccountIDFromJWT(rr.AccessToken)
        }
        if rr.ExpiresIn > 0 {
            auth.ExpiresAt = time.Now().Unix() + rr.ExpiresIn
        } else {
            auth.ExpiresAt = extractJWTExpiry(rr.AccessToken)
        }
        return save()
    }
    return errors.New("maximum refresh attempts exceeded")
}

func redactAuthError(body []byte) string {
    text := strings.TrimSpace(string(body))
    if len(text) > 200 {
        return text[:200] + "…"
    }
    return text
}

'''
    text = replace_between(text, "// codexRefreshToken uses", "// -----------------------------------------------------------------------", refresh_impl, "safe token refresh")

    load_impl = '''func loadOfficialCodexAuth() (*CodexAuthState, error) {
    path, err := officialAuthJSONPath()
    if err != nil {
        return nil, err
    }
    data, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, fmt.Errorf("read %s: %w", path, err)
    }
    var aj officialCodexAuthJSON
    if err := json.Unmarshal(data, &aj); err != nil {
        return nil, fmt.Errorf("parse %s: %w", path, err)
    }
    if aj.OpenAIAPIKey != nil && *aj.OpenAIAPIKey != "" {
        return &CodexAuthState{Mode: "api_key", APIKey: *aj.OpenAIAPIKey, APIKeyEnv: "OPENAI_API_KEY"}, nil
    }
    if aj.Tokens == nil || aj.Tokens.AccessToken == "" {
        return nil, nil
    }
    state := &CodexAuthState{
        Mode:         "chatgpt",
        AccessToken:  aj.Tokens.AccessToken,
        RefreshToken: aj.Tokens.RefreshToken,
        ExpiresAt:    aj.Tokens.ExpiresAt,
    }
    if state.ExpiresAt == 0 {
        state.ExpiresAt = extractJWTExpiry(state.AccessToken)
    }
    if aj.Tokens.AccountID != nil && *aj.Tokens.AccountID != "" {
        state.AccountID = *aj.Tokens.AccountID
    } else if aj.Tokens.IDToken != "" {
        state.AccountID = extractAccountIDFromJWT(aj.Tokens.IDToken)
    }
    if state.AccountID == "" {
        state.AccountID = extractAccountIDFromJWT(state.AccessToken)
    }
    log.Printf("[codex] imported official auth metadata: has_token=true has_account_metadata=%t", state.AccountID != "")
    return state, nil
}

'''
    text = replace_between(text, "func loadOfficialCodexAuth(", "// extractAccountIDFromJWT", load_impl, "read-only official import")

    jwt_impl = '''// extractAccountIDFromJWT parses an unverified local JWT payload only to
// obtain routing metadata. The token signature remains the upstream's concern.
func extractAccountIDFromJWT(jwt string) string {
    payload, ok := decodeJWTPayload(jwt)
    if !ok {
        return ""
    }
    var claims struct {
        AccountID string `json:"chatgpt_account_id"`
        Auth *struct {
            AccountID string `json:"chatgpt_account_id"`
        } `json:"https://api.openai.com/auth"`
    }
    if json.Unmarshal(payload, &claims) != nil {
        return ""
    }
    if claims.AccountID != "" {
        return claims.AccountID
    }
    if claims.Auth != nil {
        return claims.Auth.AccountID
    }
    return ""
}

func extractJWTExpiry(jwt string) int64 {
    payload, ok := decodeJWTPayload(jwt)
    if !ok {
        return 0
    }
    var claims struct { Exp int64 `json:"exp"` }
    if json.Unmarshal(payload, &claims) != nil {
        return 0
    }
    return claims.Exp
}

func decodeJWTPayload(jwt string) ([]byte, bool) {
    parts := strings.SplitN(jwt, ".", 3)
    if len(parts) != 3 || parts[1] == "" {
        return nil, false
    }
    payload := parts[1]
    if m := len(payload) % 4; m != 0 {
        payload += strings.Repeat("=", 4-m)
    }
    decoded, err := base64.URLEncoding.DecodeString(payload)
    if err != nil {
        return nil, false
    }
    return decoded, true
}

'''
    text = replace_between(text, "// extractAccountIDFromJWT", "// persistToOfficialStore", jwt_impl, "JWT metadata")

    text = text.replace(
        "\tif tokens.ExpiresIn > 0 {\n\t\tauth.ExpiresAt = time.Now().Unix() + tokens.ExpiresIn\n\t} else {\n\t\tauth.ExpiresAt = time.Now().Unix() + 3600\n\t}\n",
        "\tif tokens.ExpiresIn > 0 {\n\t\tauth.ExpiresAt = time.Now().Unix() + tokens.ExpiresIn\n\t} else {\n\t\tauth.ExpiresAt = extractJWTExpiry(tokens.AccessToken)\n\t}\n\tif auth.AccountID == \"\" {\n\t\tauth.AccountID = extractAccountIDFromJWT(tokens.AccessToken)\n\t}\n",
    )
    write(path, text)


def patch_responses_adapter() -> None:
    path = "src/responses_adapter.go"
    text = read(path)

    conversion_impl = r'''// ---------------------------------------------------------------------------
// Request conversion: Chat Completions and native Responses requests
// ---------------------------------------------------------------------------

var chatGPTCodexAllowedKeys = map[string]bool{
    "model": true, "input": true, "instructions": true, "tools": true,
    "tool_choice": true, "parallel_tool_calls": true, "text": true,
    "reasoning": true, "metadata": true, "stream": true, "store": true,
    "temperature": true, "top_p": true, "user": true,
}

func responseFormatToResponsesText(raw json.RawMessage) (json.RawMessage, error) {
    var envelope struct {
        Type       string          `json:"type"`
        JSONSchema json.RawMessage `json:"json_schema"`
    }
    if err := json.Unmarshal(raw, &envelope); err != nil {
        return nil, fmt.Errorf("parse response_format: %w", err)
    }
    var format map[string]json.RawMessage
    switch envelope.Type {
    case "json_schema":
        if len(envelope.JSONSchema) == 0 || string(envelope.JSONSchema) == "null" {
            return nil, fmt.Errorf("response_format.json_schema is required")
        }
        if err := json.Unmarshal(envelope.JSONSchema, &format); err != nil {
            return nil, fmt.Errorf("parse response_format.json_schema: %w", err)
        }
        format["type"], _ = json.Marshal("json_schema")
    case "json_object":
        format = map[string]json.RawMessage{"type": json.RawMessage(`"json_object"`)}
    case "text":
        format = map[string]json.RawMessage{"type": json.RawMessage(`"text"`)}
    default:
        return nil, fmt.Errorf("unsupported response_format type %q", envelope.Type)
    }
    formatJSON, err := json.Marshal(format)
    if err != nil {
        return nil, err
    }
    return json.Marshal(map[string]json.RawMessage{"format": formatJSON})
}

func buildChatGPTCodexRequest(chatBody []byte) ([]byte, bool, error) {
    var raw map[string]json.RawMessage
    if err := json.Unmarshal(chatBody, &raw); err != nil {
        return nil, false, fmt.Errorf("unmarshal chat body: %w", err)
    }
    includeUsage := streamOptionsIncludeUsage(raw)
    out := make(map[string]json.RawMessage, len(chatGPTCodexAllowedKeys))
    if v, ok := raw["messages"]; ok {
        instructions, inputJSON, err := extractMessages(v)
        if err != nil {
            return nil, false, err
        }
        out["instructions"], _ = json.Marshal(instructions)
        out["input"] = inputJSON
    }
    if _, ok := out["instructions"]; !ok {
        out["instructions"], _ = json.Marshal("")
    }
    for _, key := range []string{"model", "temperature", "top_p", "user", "tool_choice", "parallel_tool_calls", "reasoning", "metadata"} {
        if value, ok := raw[key]; ok {
            out[key] = value
        }
    }
    if tools, ok := raw["tools"]; ok {
        out["tools"] = convertToolsForResponses(tools)
    }
    if responseFormat, ok := raw["response_format"]; ok {
        textFormat, err := responseFormatToResponsesText(responseFormat)
        if err != nil {
            return nil, false, err
        }
        out["text"] = textFormat
    }
    out["stream"], _ = json.Marshal(true)
    out["store"], _ = json.Marshal(false)
    body, err := json.Marshal(out)
    return body, includeUsage, err
}

func buildPlatformResponsesRequest(chatBody []byte) ([]byte, bool, error) {
    var raw map[string]json.RawMessage
    if err := json.Unmarshal(chatBody, &raw); err != nil {
        return nil, false, fmt.Errorf("unmarshal chat body: %w", err)
    }
    includeUsage := streamOptionsIncludeUsage(raw)
    out := make(map[string]json.RawMessage, len(raw)+2)
    for key, value := range raw {
        switch key {
        case "messages":
            instructions, inputJSON, err := extractMessages(value)
            if err != nil {
                return nil, false, err
            }
            out["instructions"], _ = json.Marshal(instructions)
            out["input"] = inputJSON
        case "max_tokens", "max_completion_tokens":
            out["max_output_tokens"] = value
        case "stream_options":
        case "tools":
            out["tools"] = convertToolsForResponses(value)
        case "response_format":
            textFormat, err := responseFormatToResponsesText(value)
            if err != nil {
                return nil, false, err
            }
            out["text"] = textFormat
        case "n", "stop", "logprobs", "top_logprobs", "logit_bias",
            "frequency_penalty", "presence_penalty", "seed", "function_call", "functions":
        default:
            out[key] = value
        }
    }
    if _, ok := out["instructions"]; !ok {
        out["instructions"], _ = json.Marshal("")
    }
    body, err := json.Marshal(out)
    return body, includeUsage, err
}

func buildChatGPTNativeResponsesRequest(body []byte) ([]byte, bool, error) {
    var raw map[string]json.RawMessage
    if err := json.Unmarshal(body, &raw); err != nil {
        return nil, false, fmt.Errorf("unmarshal responses body: %w", err)
    }
    clientWantsStream := false
    if stream, ok := raw["stream"]; ok {
        _ = json.Unmarshal(stream, &clientWantsStream)
    }
    out := make(map[string]json.RawMessage, len(chatGPTCodexAllowedKeys))
    for key, value := range raw {
        if chatGPTCodexAllowedKeys[key] {
            out[key] = value
        }
    }
    out["stream"], _ = json.Marshal(true)
    out["store"], _ = json.Marshal(false)
    encoded, err := json.Marshal(out)
    return encoded, clientWantsStream, err
}

func buildPlatformNativeResponsesRequest(body []byte) ([]byte, bool, error) {
    var raw map[string]json.RawMessage
    if err := json.Unmarshal(body, &raw); err != nil {
        return nil, false, fmt.Errorf("unmarshal responses body: %w", err)
    }
    clientWantsStream := false
    if stream, ok := raw["stream"]; ok {
        _ = json.Unmarshal(stream, &clientWantsStream)
    }
    delete(raw, "stream_options")
    encoded, err := json.Marshal(raw)
    return encoded, clientWantsStream, err
}

'''
    text = replace_between(text, "// ---------------------------------------------------------------------------\n// Request conversion:", "// ---------------------------------------------------------------------------\n// Non-streaming response conversion", conversion_impl, "structured output conversion")
    text = text.replace('\tw.Header().Set("Access-Control-Allow-Origin", "*")\n', "")
    text = text.replace('\tw.Header().Set("Access-Control-Allow-Headers", "*")\n', "")
    text = text.replace("512*1024", "4*1024*1024")
    text = text.replace(
        '\t\tcase "error":\n\t\t\t// Forward error as an SSE error event.\n\t\t\tlog.Printf("[responses_adapter] upstream error event: %s", data)\n\t\t\t// Convert to chat completion error chunk.\n\t\t\tfmt.Fprintf(w, "data: %s\\n\\n", data)\n\t\t\tflusher.Flush()\n\t\t\tfmt.Fprintf(w, "data: [DONE]\\n\\n")\n\t\t\tflusher.Flush()\n\t\t\treturn nil\n',
        '\t\tcase "error":\n\t\t\tlog.Printf("[responses_adapter] upstream error event received")\n\t\t\tfmt.Fprintf(w, "data: %s\\n\\n", data)\n\t\t\tflusher.Flush()\n\t\t\tfmt.Fprintf(w, "data: [DONE]\\n\\n")\n\t\t\tflusher.Flush()\n\t\t\treturn fmt.Errorf("upstream SSE error")\n',
    )

    native_handler = '''

func handleNativeResponsesAPIResponse(w http.ResponseWriter, resp *http.Response, clientWantsStream, upstreamSSE bool) error {
    defer resp.Body.Close()
    contentType := resp.Header.Get("Content-Type")
    isSSE := strings.Contains(contentType, "text/event-stream") || upstreamSSE
    if resp.StatusCode >= 400 {
        body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(resp.StatusCode)
        _, _ = w.Write(body)
        kind := ProviderErrorUnavailable
        if resp.StatusCode == http.StatusUnauthorized {
            kind = ProviderErrorAuthRequired
        } else if resp.StatusCode == http.StatusForbidden {
            kind = ProviderErrorEntitlement
        } else if resp.StatusCode == http.StatusTooManyRequests {
            kind = ProviderErrorRateLimit
        }
        return newProviderError(ProviderCodex, kind, resp.StatusCode, resp.StatusCode >= 500, "upstream HTTP %d", resp.StatusCode)
    }
    if isSSE && !clientWantsStream {
        responseJSON, err := aggregateSSEToCompletion(resp.Body)
        if err != nil {
            return err
        }
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        _, err = w.Write(responseJSON)
        return err
    }
    if isSSE {
        w.Header().Set("Content-Type", "text/event-stream")
        w.Header().Set("Cache-Control", "no-cache")
        w.WriteHeader(http.StatusOK)
        flusher, ok := w.(http.Flusher)
        if !ok {
            return fmt.Errorf("ResponseWriter does not support Flusher")
        }
        buffer := make([]byte, 32*1024)
        for {
            n, err := resp.Body.Read(buffer)
            if n > 0 {
                if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
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
    copyHeaders(w, resp.Header, "Content-Length")
    w.WriteHeader(resp.StatusCode)
    _, err := io.Copy(w, resp.Body)
    return err
}
'''
    if "func handleNativeResponsesAPIResponse" not in text:
        text += native_handler
    write(path, text)


def patch_codex_provider() -> None:
    path = "src/codex_provider.go"
    text = read(path)
    text = replace_once(text, '"net/http"\n\t"sync"', '"net/http"\n\t"strings"\n\t"sync"', "codex strings import")

    constructor_impl = '''// NewCodexProvider constructs a CodexProvider from cfg.
func NewCodexProvider(cfg *Config) *CodexProvider {
    p := &CodexProvider{
        cfg: cfg,
        cb: &CircuitBreaker{state: CircuitClosed, timeout: time.Duration(cfg.Timeouts.CircuitBreaker) * time.Second},
        cache: NewModelCache(defaultModelCacheTTL),
    }
    p.accountCursor = p.firstHealthyCodexAccount()
    return p
}

func (p *CodexProvider) ID() ProviderID { return ProviderCodex }
func (p *CodexProvider) Name() string   { return "OpenAI Codex" }
func (p *CodexProvider) Capabilities() []Capability {
    p.mu.Lock()
    defer p.mu.Unlock()
    auth := p.authState()
    if isAPIKeyMode(auth.Mode) {
        return []Capability{CapabilityChat, CapabilityResponses, CapabilityEmbeddings}
    }
    return []Capability{CapabilityChat, CapabilityResponses}
}

func (p *CodexProvider) chatGPTBaseURL() string {
    base := strings.TrimSpace(p.cfg.Providers.Codex.ChatGPTBaseURL)
    if base == "" {
        base = defaultChatGPTBaseURL
    }
    if !strings.HasSuffix(base, "/") {
        base += "/"
    }
    return base
}

'''
    text = replace_between(text, "// NewCodexProvider constructs", "func (p *CodexProvider) activeAccount", constructor_impl, "dynamic Codex capabilities")

    reload_impl = '''// reloadTokenFromDisk refreshes the active account from ya-router's config.
func (p *CodexProvider) reloadTokenFromDisk() {
    fresh, err := loadConfig()
    if err != nil {
        log.Printf("[codex] config reload failed: %v", err)
        return
    }
    current := p.activeAccount()
    if current != nil {
        for i := range fresh.Providers.Codex.Accounts {
            candidate := &fresh.Providers.Codex.Accounts[i]
            if candidate.Label == current.Label && candidate.Auth.AccessToken != "" {
                current.Auth = candidate.Auth
                return
            }
        }
        return
    }
    if fresh.Providers.Codex.Auth.AccessToken != "" {
        p.cfg.Providers.Codex.Auth = fresh.Providers.Codex.Auth
    }
}

// reloadFromOfficialStore performs a read-only import only when the active
// ya-router credential is empty; it never replaces an account-owned token.
func (p *CodexProvider) reloadFromOfficialStore() {
    auth := p.authState()
    if auth.AccessToken != "" {
        return
    }
    official, err := loadOfficialCodexAuth()
    if err != nil {
        log.Printf("[codex] official store import failed: %v", err)
        return
    }
    if official != nil && official.AccessToken != "" {
        applyResolvedCodexChatGPTAuth(auth, &resolvedCodexChatGPTAuth{
            AccessToken: official.AccessToken, RefreshToken: official.RefreshToken,
            ExpiresAt: official.ExpiresAt, AccountID: official.AccountID,
            Source: codexCredentialSourceOfficialStore,
        })
    }
}

'''
    text = replace_between(text, "// reloadTokenFromDisk", "// EnsureAuthenticated", reload_impl, "account credential isolation")

    ensure_impl = '''// EnsureAuthenticated ensures a valid credential for the selected account.
// The provider mutex also acts as a single-flight gate for token refresh.
func (p *CodexProvider) EnsureAuthenticated(_ context.Context) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    auth := p.authState()
    if isAPIKeyMode(auth.Mode) {
        if key, _, err := resolveCodexAPIKey(auth); err == nil && key != "" {
            return nil
        }
        return newProviderError(ProviderCodex, ProviderErrorAuthRequired, http.StatusUnauthorized, false, "no Codex API key configured")
    }

    if auth.AccessToken == "" {
        p.reloadTokenFromDisk()
    }
    if auth.AccessToken == "" && len(p.cfg.Providers.Codex.Accounts) <= 1 {
        p.reloadFromOfficialStore()
    }
    if auth.AccessToken == "" {
        return newProviderError(ProviderCodex, ProviderErrorAuthRequired, http.StatusUnauthorized, false, "no Codex token; run 'auth codex'")
    }
    if auth.AccountID == "" {
        auth.AccountID = extractAccountIDFromJWT(auth.AccessToken)
    }
    if auth.ExpiresAt == 0 {
        auth.ExpiresAt = extractJWTExpiry(auth.AccessToken)
    }

    now := time.Now().Unix()
    if auth.ExpiresAt > 0 && auth.ExpiresAt-now <= 300 {
        if err := codexRefreshToken(auth, p.save); err != nil {
            if auth.ExpiresAt <= now {
                return newProviderError(ProviderCodex, ProviderErrorAuthRequired, http.StatusUnauthorized, false, "Codex token expired and refresh failed: %v", err)
            }
            log.Printf("[codex] proactive refresh failed; current token remains usable: %v", err)
        }
    }
    return nil
}

func (p *CodexProvider) forceRefreshActiveAccount() error {
    p.mu.Lock()
    defer p.mu.Unlock()
    auth := p.authState()
    if isAPIKeyMode(auth.Mode) {
        return errors.New("API-key credentials do not refresh")
    }
    return codexRefreshToken(auth, p.save)
}

'''
    # Add errors import for forceRefresh.
    text = replace_once(text, '"context"\n\t"fmt"', '"context"\n\t"errors"\n\t"fmt"', "codex errors import")
    text = replace_between(text, "// EnsureAuthenticated ensures", "// codexKnownModels", ensure_impl, "bounded auth lifecycle")

    proxy_impl = '''// ProxyRequest executes Chat Completions, native Responses, or embeddings.
// ChatGPT OAuth credentials are never sent to OpenAI Platform endpoints.
func (p *CodexProvider) ProxyRequest(
    ctx context.Context,
    w http.ResponseWriter,
    r *http.Request,
    body []byte,
    cap Capability,
) error {
    p.mu.Lock()
    maxAccountAttempts := len(p.cfg.Providers.Codex.Accounts)
    if maxAccountAttempts < 1 {
        maxAccountAttempts = 1
    }
    p.mu.Unlock()

    for accountAttempt := 0; accountAttempt < maxAccountAttempts; accountAttempt++ {
        if err := p.EnsureAuthenticated(ctx); err != nil {
            return err
        }
        if !p.cb.canExecute() {
            return newProviderError(ProviderCodex, ProviderErrorUnavailable, http.StatusServiceUnavailable, true, "Codex circuit breaker is open")
        }
        token, accountID, chatgpt := p.authCredentials()
        if token == "" {
            return newProviderError(ProviderCodex, ProviderErrorAuthRequired, http.StatusUnauthorized, false, "Codex credential is empty")
        }

        if cap == CapabilityEmbeddings {
            if chatgpt {
                return newProviderError(ProviderCodex, ProviderErrorUnsupported, http.StatusBadRequest, false, "embeddings require OpenAI Platform API-key mode")
            }
            return p.proxyClassic(ctx, w, r, body, "/embeddings", token)
        }
        if cap != CapabilityChat && cap != CapabilityResponses {
            return newProviderError(ProviderCodex, ProviderErrorUnsupported, http.StatusBadRequest, false, "unsupported Codex capability %q", cap)
        }

        nativeResponses := cap == CapabilityResponses
        var upstreamBody []byte
        var clientWantsStream bool
        var includeUsage bool
        var upstreamURL string
        var buildErr error
        if chatgpt {
            upstreamURL = p.chatGPTBaseURL() + "responses"
            if nativeResponses {
                upstreamBody, clientWantsStream, buildErr = buildChatGPTNativeResponsesRequest(body)
            } else {
                upstreamBody, includeUsage, buildErr = buildChatGPTCodexRequest(body)
                clientWantsStream = isStreamingRequest(body)
            }
        } else {
            upstreamURL = defaultPlatformBaseURL + "/responses"
            if nativeResponses {
                upstreamBody, clientWantsStream, buildErr = buildPlatformNativeResponsesRequest(body)
            } else {
                upstreamBody, includeUsage, buildErr = buildPlatformResponsesRequest(body)
                clientWantsStream = isStreamingRequest(body)
            }
        }
        if buildErr != nil {
            return newProviderError(ProviderCodex, ProviderErrorInvalidRequest, http.StatusBadRequest, false, "build upstream request: %v", buildErr)
        }

        var resp *http.Response
        var err error
        for authAttempt := 0; authAttempt < 2; authAttempt++ {
            req, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewBuffer(upstreamBody))
            if requestErr != nil {
                return requestErr
            }
            if chatgpt {
                setChatGPTHeaders(req, token, accountID)
            } else {
                setPlatformHeaders(req, token)
            }
            if p.proxyExecutor != nil {
                resp, _, err = p.proxyExecutor(ctx, req, upstreamBody, cap)
            } else {
                resp, err = makeRequestWithRetry(sharedHTTPClient, req, upstreamBody)
            }
            if err != nil {
                p.cb.onFailure()
                return newProviderError(ProviderCodex, ProviderErrorTransport, http.StatusBadGateway, true, "Codex upstream request failed: %v", err)
            }
            if resp.StatusCode == http.StatusUnauthorized && chatgpt && authAttempt == 0 {
                resp.Body.Close()
                if refreshErr := p.forceRefreshActiveAccount(); refreshErr != nil {
                    return newProviderError(ProviderCodex, ProviderErrorAuthRequired, http.StatusUnauthorized, false, "Codex authentication expired: %v", refreshErr)
                }
                token, accountID, _ = p.authCredentials()
                continue
            }
            break
        }
        if resp == nil {
            return newProviderError(ProviderCodex, ProviderErrorTransport, http.StatusBadGateway, true, "Codex returned no response")
        }

        isLimit, limitReason := isAccountLimitSignal(resp)
        if isLimit && maxAccountAttempts > 1 {
            p.mu.Lock()
            advanced := p.advanceCodexAccount()
            p.mu.Unlock()
            if advanced {
                log.Printf("[codex] account limit signal (%s); advancing account", limitReason)
                resp.Body.Close()
                continue
            }
        }

        if resp.StatusCode >= 500 {
            p.cb.onFailure()
        } else {
            p.cb.onSuccess()
        }
        if nativeResponses {
            return handleNativeResponsesAPIResponse(w, resp, clientWantsStream, chatgpt)
        }
        return handleResponsesAPIResponse(w, resp, clientWantsStream, chatgpt, includeUsage)
    }
    return newProviderError(ProviderCodex, ProviderErrorRateLimit, http.StatusTooManyRequests, true, "all Codex accounts are rate limited")
}

'''
    text = replace_between(text, "// ProxyRequest proxies", "// proxyClassic", proxy_impl, "Codex transport boundary")

    health_impl = '''// Health returns the selected account's authentication state.
func (p *CodexProvider) Health(_ context.Context) ProviderHealth {
    p.mu.Lock()
    defer p.mu.Unlock()
    auth := p.authState()
    if isAPIKeyMode(auth.Mode) {
        key, _, err := resolveCodexAPIKey(auth)
        return ProviderHealth{Authenticated: err == nil && key != "", CanRefresh: false}
    }
    authenticated := auth.AccessToken != ""
    if auth.ExpiresAt > 0 {
        authenticated = authenticated && auth.ExpiresAt > time.Now().Unix()
    }
    return ProviderHealth{Authenticated: authenticated, CanRefresh: auth.RefreshToken != ""}
}
'''
    text = replace_between(text, "// Health returns", None, health_impl, "Codex health")
    write(path, text)


def patch_tests() -> None:
    write(
        "src/hardening_test.go",
        '''package main

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"
)

type hardeningProvider struct {
    id       ProviderID
    caps     []Capability
    models   []Model
    calls    int
    freeCalls int
}

func (p *hardeningProvider) ID() ProviderID { return p.id }
func (p *hardeningProvider) Name() string { return string(p.id) }
func (p *hardeningProvider) Capabilities() []Capability { return p.caps }
func (p *hardeningProvider) EnsureAuthenticated(context.Context) error { return nil }
func (p *hardeningProvider) ListModels(context.Context) (*ModelList, error) {
    return &ModelList{Object: "list", Data: p.models}, nil
}
func (p *hardeningProvider) ProxyRequest(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
    p.calls++
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte(`{"ok":true}`))
    return nil
}
func (p *hardeningProvider) ProxyFreeChatRequest(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ string) error {
    p.freeCalls++
    w.WriteHeader(http.StatusOK)
    return nil
}
func (p *hardeningProvider) Health(context.Context) ProviderHealth { return ProviderHealth{Authenticated: true} }

func TestProcessProxyRequestHonorsProviderPrefixBeforeCopilotFastPath(t *testing.T) {
    registry := NewProviderRegistry()
    copilot := &hardeningProvider{id: ProviderCopilot, caps: []Capability{CapabilityChat}, models: []Model{{ID: "gpt-test"}}}
    codex := &hardeningProvider{id: ProviderCodex, caps: []Capability{CapabilityChat}, models: []Model{{ID: "gpt-test"}}}
    registry.Register(copilot)
    registry.Register(codex)
    router := NewModelRouter(registry, defaultConfig().Routing)
    request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"oc-gpt-test","messages":[]}`))
    response := httptest.NewRecorder()
    if err := processProxyRequest(registry, router, defaultConfig(), response, request, context.Background()); err != nil {
        t.Fatalf("processProxyRequest: %v", err)
    }
    if codex.calls != 1 || copilot.freeCalls != 0 {
        t.Fatalf("codex calls=%d copilot free calls=%d", codex.calls, copilot.freeCalls)
    }
}

func TestConfiguredListenAddressDefaultsToLoopback(t *testing.T) {
    t.Setenv(listenAddressEnv, "")
    t.Setenv(inboundAPIKeyEnv, "")
    address, err := configuredListenAddress(7071)
    if err != nil || address != "127.0.0.1:7071" {
        t.Fatalf("address=%q err=%v", address, err)
    }
}

func TestConfiguredListenAddressRequiresKeyForPublicBind(t *testing.T) {
    t.Setenv(listenAddressEnv, "0.0.0.0")
    t.Setenv(inboundAPIKeyEnv, "")
    if _, err := configuredListenAddress(7071); err == nil {
        t.Fatal("expected public bind without API key to fail")
    }
}

func TestStructuredOutputIsTranslatedToResponsesTextFormat(t *testing.T) {
    request := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}}}`)
    body, _, err := buildChatGPTCodexRequest(request)
    if err != nil {
        t.Fatalf("buildChatGPTCodexRequest: %v", err)
    }
    var decoded map[string]json.RawMessage
    if err := json.Unmarshal(body, &decoded); err != nil {
        t.Fatal(err)
    }
    if _, ok := decoded["text"]; !ok {
        t.Fatalf("structured output was dropped: %s", body)
    }
}

func TestCodexCapabilitiesDependOnAuthMode(t *testing.T) {
    cfg := defaultConfig()
    cfg.Providers.Codex.Auth = CodexAuthState{Mode: "chatgpt", AccessToken: "token"}
    provider := NewCodexProvider(cfg)
    for _, capability := range provider.Capabilities() {
        if capability == CapabilityEmbeddings {
            t.Fatal("ChatGPT mode must not advertise embeddings")
        }
    }
    cfg.Providers.Codex.Auth.Mode = "api_key"
    cfg.Providers.Codex.Auth.APIKey = "test"
    found := false
    for _, capability := range provider.Capabilities() {
        found = found || capability == CapabilityEmbeddings
    }
    if !found {
        t.Fatal("API-key mode should advertise embeddings")
    }
}

func TestChatGPTModeRejectsEmbeddingsWithoutOutboundCall(t *testing.T) {
    cfg := defaultConfig()
    cfg.Providers.Codex.Enabled = true
    cfg.Providers.Codex.Auth = CodexAuthState{Mode: "chatgpt", AccessToken: "token", AccountID: "account", ExpiresAt: time.Now().Unix() + 3600}
    provider := NewCodexProvider(cfg)
    calls := 0
    provider.proxyExecutor = func(context.Context, *http.Request, []byte, Capability) (*http.Response, string, error) {
        calls++
        return nil, "", nil
    }
    err := provider.ProxyRequest(context.Background(), httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil), []byte(`{"model":"text-embedding-3-large","input":"x"}`), CapabilityEmbeddings)
    if err == nil || calls != 0 {
        t.Fatalf("err=%v outbound calls=%d", err, calls)
    }
}

func TestSecureHandlerRejectsInvalidCredential(t *testing.T) {
    t.Setenv(inboundAPIKeyEnv, "secret")
    handler := secureHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
    request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
    response := httptest.NewRecorder()
    handler.ServeHTTP(response, request)
    if response.Code != http.StatusUnauthorized {
        t.Fatalf("status=%d", response.Code)
    }
}
''',
    )


def main() -> None:
    patch_provider()
    patch_proxy()
    patch_security_and_cli()
    patch_config_and_auth()
    patch_responses_adapter()
    patch_codex_provider()
    patch_tests()


if __name__ == "__main__":
    main()
