package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/duvu/ya-router/internal/secret"
)

func TestSecretRouteExposesOnlyRedactedMetadata(t *testing.T) {
	store := secret.NewMemoryStore(nil)
	const secretValue = "top-secret-value"
	if _, err := store.Set("admin", "kilo/api_key", secretValue); err != nil {
		t.Fatal(err)
	}
	_ = store.RegisterReadOnly("codex/api_key", "env-value", secret.SourceEnvironment)

	api := NewAPI(APIOptions{})
	RegisterSecretRoutes(api, store)
	identity := Identity{Subject: "test", Role: RoleViewer, Source: "test"}

	request := httptest.NewRequest(http.MethodGet, "/control/v1/secrets", nil)
	response := httptest.NewRecorder()
	api.Handler(FixedAuthenticator(identity)).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if strings.Contains(body, secretValue) || strings.Contains(body, "env-value") {
		t.Fatalf("secret value leaked in response: %s", body)
	}

	var payload struct {
		Data []secret.Metadata `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(payload.Data))
	}
	for _, meta := range payload.Data {
		if !meta.Configured {
			t.Fatalf("secret %q should be configured", meta.ID)
		}
	}
}

func TestSecretRouteNilReaderRegistersNothing(t *testing.T) {
	api := NewAPI(APIOptions{})
	RegisterSecretRoutes(api, nil)
	identity := Identity{Subject: "test", Role: RoleViewer, Source: "test"}
	request := httptest.NewRequest(http.MethodGet, "/control/v1/secrets", nil)
	response := httptest.NewRecorder()
	api.Handler(FixedAuthenticator(identity)).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no secret reader is registered", response.Code)
	}
}
