package yarouter

import (
	"path/filepath"
	"strings"
	"testing"

	controlpkg "github.com/duvu/ya-router/internal/control"
)

func clearControlEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		controlSocketEnv,
		controlRemoteAddressEnv,
		controlTLSCertEnv,
		controlTLSKeyEnv,
		controlClientCAEnv,
		controlRequireMTLSEnv,
		controlViewerTokenEnv,
		controlOperatorTokenEnv,
		controlAdminTokenEnv,
		controlViewerSubjectsEnv,
		controlOperatorSubjectsEnv,
		controlAdminSubjectsEnv,
		inboundAPIKeyEnv,
	} {
		t.Setenv(name, "")
	}
}

func TestConfiguredControlListenerDefaultsToPrivateUnixSocket(t *testing.T) {
	clearControlEnvironment(t)
	oldOverride := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldOverride })

	listener, tokens, subjects, err := configuredControlListener(defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(configPathOverride), "control.sock")
	if listener.UnixSocket != want || listener.UnixMode != 0o600 || listener.RemoteAddress != "" {
		t.Fatalf("unexpected local listener: %+v", listener)
	}
	if len(tokens) != 0 || len(subjects) != 0 {
		t.Fatalf("unexpected remote identities: tokens=%v subjects=%v", tokens, subjects)
	}
}

func TestControlTokenCannotReuseDataPlaneKey(t *testing.T) {
	clearControlEnvironment(t)
	oldOverride := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldOverride })
	t.Setenv(inboundAPIKeyEnv, "shared-secret")
	t.Setenv(controlAdminTokenEnv, "shared-secret")

	_, _, _, err := configuredControlListener(defaultConfig())
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("expected independent credential error, got %v", err)
	}
}

func TestLoopbackRemoteControlAllowsIndependentTokenOverTLS(t *testing.T) {
	clearControlEnvironment(t)
	oldOverride := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldOverride })
	t.Setenv(controlSocketEnv, "off")
	t.Setenv(controlRemoteAddressEnv, "127.0.0.1:7443")
	t.Setenv(controlTLSCertEnv, "server.pem")
	t.Setenv(controlTLSKeyEnv, "server-key.pem")
	t.Setenv(controlAdminTokenEnv, "control-only-token")

	listener, tokens, _, err := configuredControlListener(defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if listener.RequireMTLS || listener.RemoteAddress != "127.0.0.1:7443" || tokens[controlpkg.RoleAdmin] != "control-only-token" {
		t.Fatalf("unexpected loopback remote config: %+v tokens=%v", listener, tokens)
	}
}

func TestNonLoopbackRemoteControlRequiresMTLSRoleMapping(t *testing.T) {
	clearControlEnvironment(t)
	oldOverride := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldOverride })
	t.Setenv(controlSocketEnv, "off")
	t.Setenv(controlRemoteAddressEnv, "0.0.0.0:7443")
	t.Setenv(controlTLSCertEnv, "server.pem")
	t.Setenv(controlTLSKeyEnv, "server-key.pem")
	t.Setenv(controlClientCAEnv, "client-ca.pem")
	t.Setenv(controlAdminTokenEnv, "control-token")

	_, _, _, err := configuredControlListener(defaultConfig())
	if err == nil || !strings.Contains(err.Error(), "subject-to-role mapping") {
		t.Fatalf("expected mTLS role mapping error, got %v", err)
	}
	t.Setenv(controlAdminSubjectsEnv, "spiffe://example/admin")
	listener, _, subjects, err := configuredControlListener(defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !listener.RequireMTLS || subjects["spiffe://example/admin"] != controlpkg.RoleAdmin {
		t.Fatalf("unexpected mTLS config: %+v subjects=%v", listener, subjects)
	}
}

func TestDisablingAllControlListenersFailsClosed(t *testing.T) {
	clearControlEnvironment(t)
	oldOverride := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldOverride })
	t.Setenv(controlSocketEnv, "disabled")

	_, _, _, err := configuredControlListener(defaultConfig())
	if err == nil || !strings.Contains(err.Error(), "at least one control listener") {
		t.Fatalf("expected fail-closed listener error, got %v", err)
	}
}
