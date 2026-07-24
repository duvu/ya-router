package yarouter

import (
	"reflect"
	"testing"
)

func TestDefaultThienduUsesSparkFirstQuotaPriority(t *testing.T) {
	cfg := defaultConfig()
	vm, ok := cfg.Routing.VirtualModels["thiendu"]
	if !ok {
		t.Fatal("default thiendu virtual model is missing")
	}
	if vm.Strategy != "quota_priority" {
		t.Fatalf("thiendu strategy = %q, want quota_priority", vm.Strategy)
	}
	want := []string{
		"codex/gpt-5.3-codex-spark",
		"codex/gpt-5.4-mini",
		"github/gpt-5.4-mini",
		"github/gpt-5-mini",
		"kilo/kilo-auto/free",
	}
	if !reflect.DeepEqual(vm.Targets, want) {
		t.Fatalf("thiendu targets = %#v, want %#v", vm.Targets, want)
	}
}
