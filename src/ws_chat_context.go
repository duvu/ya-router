// ws_chat_context.go carries an optional callback through the request
// context so the WS chat path (issue #76) can observe which provider/model
// processProxyRequest selected — without adding a second router or a
// parallel dispatch path. HTTP callers never set this; it is a no-op unless
// wsChatHandler installs it.
package yarouter

import "context"

type routeObserverContextKey struct{}

// routeObserverFunc is called exactly once per request, right after routing
// resolves and before any provider I/O, with the selected provider ID and
// resolved upstream model.
type routeObserverFunc func(providerID ProviderID, resolvedModel string)

func withRouteObserver(ctx context.Context, observer routeObserverFunc) context.Context {
	return context.WithValue(ctx, routeObserverContextKey{}, observer)
}

func routeObserverFromContext(ctx context.Context) routeObserverFunc {
	observer, _ := ctx.Value(routeObserverContextKey{}).(routeObserverFunc)
	return observer
}
