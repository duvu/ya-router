// Package runtime defines the service composition boundary used by binaries
// and the future RuntimeManager.
package runtime

import (
	"fmt"

	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
	"github.com/duvu/ya-router/internal/routing"
)

// Components groups one service instance's explicit dependencies. It is not
// a mutable runtime manager; YA-TUI-02 will add snapshot publication and drain.
type Components struct {
	Config    *configschema.Config
	Providers *provider.Registry
	Router    *routing.Router
}

// NewComponents validates and returns an isolated service composition.
func NewComponents(config *configschema.Config, providers *provider.Registry, router *routing.Router) (*Components, error) {
	if config == nil {
		return nil, fmt.Errorf("runtime config is required")
	}
	if providers == nil {
		return nil, fmt.Errorf("provider registry is required")
	}
	if router == nil {
		return nil, fmt.Errorf("model router is required")
	}
	return &Components{Config: config, Providers: providers, Router: router}, nil
}
