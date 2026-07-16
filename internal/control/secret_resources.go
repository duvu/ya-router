package control

import (
	"net/http"

	"github.com/duvu/ya-router/internal/secret"
)

// SecretMetadataReader exposes only redacted secret posture. Implementations
// must never return secret values through this interface.
type SecretMetadataReader interface {
	List() []secret.Metadata
}

// RegisterSecretRoutes adds the redacted secret-metadata read route. Values are
// never returned: the control plane exposes only configured/source/read-only
// posture so clients can display credential state without ever receiving a
// secret. Viewer role suffices because no value is disclosed.
func RegisterSecretRoutes(api *API, reader SecretMetadataReader) {
	if reader == nil {
		return
	}
	api.Handle(http.MethodGet, "/control/v1/secrets", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		metadata := reader.List()
		if metadata == nil {
			metadata = []secret.Metadata{}
		}
		writeJSON(writer, http.StatusOK, map[string]any{"data": metadata})
	}))
}
