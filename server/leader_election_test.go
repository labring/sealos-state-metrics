package server

import (
	"strings"
	"testing"
)

func TestCollectorLeaseName(t *testing.T) {
	got := collectorLeaseName("sealos-state-metrics", "domain")
	if got != "sealos-state-metrics-domain" {
		t.Fatalf("unexpected lease name: %s", got)
	}
}

func TestCollectorLeaseNameTruncatesToKubernetesLimit(t *testing.T) {
	got := collectorLeaseName(strings.Repeat("a", 50), strings.Repeat("b", 30))
	if len(got) > 63 {
		t.Fatalf("lease name exceeds Kubernetes limit: %d", len(got))
	}

	if !strings.Contains(got, "bbbb") {
		t.Fatalf("expected collector suffix to remain recognizable, got: %s", got)
	}
}
