//nolint:testpackage
package imagepull

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/labring/sealos-state-metrics/pkg/collector/base"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

func TestFailureClassifierMatchesLegacyRules(t *testing.T) {
	t.Parallel()

	classifier := NewFailureClassifier()

	tests := []struct {
		name     string
		reason   string
		message  string
		expected FailureReason
	}{
		{
			name:     "image not found",
			reason:   "ErrImagePull",
			message:  "rpc error: code = NotFound desc = failed to pull and unpack image: manifest unknown",
			expected: FailureReasonImageNotFound,
		},
		{
			name:     "proxy error",
			reason:   "ErrImagePull",
			message:  "proxyconnect tcp: proxy error",
			expected: FailureReasonProxyError,
		},
		{
			name:     "unauthorized",
			reason:   "ErrImagePull",
			message:  "pull access denied, repository does not exist or may require authorization: authorization failed",
			expected: FailureReasonImageNotFound,
		},
		{
			name:     "tls handshake",
			reason:   "ErrImagePull",
			message:  "Get https://registry.example/v2/: tls handshake timeout",
			expected: FailureReasonTLSHandshake,
		},
		{
			name:     "connection refused",
			reason:   "ErrImagePull",
			message:  "dial tcp 1.2.3.4:443: connect: connection refused",
			expected: FailureReasonConnectionRefused,
		},
		{
			name:     "network error",
			reason:   "ErrImagePull",
			message:  "failed to do request: Head https://registry.example/v2/",
			expected: FailureReasonNetworkError,
		},
		{
			name:     "backoff fallback",
			reason:   "ImagePullBackOff",
			message:  "Back-off pulling image \"nginx:bad\"",
			expected: FailureReasonBackOff,
		},
		{
			name:     "unknown non-pull reason stays lowercase",
			reason:   "RegistryUnavailable",
			message:  "registry unavailable",
			expected: FailureReason("registryunavailable"),
		},
		{
			name:     "invalid image name stays explicit",
			reason:   "InvalidImageName",
			message:  "Failed to apply default image tag",
			expected: FailureReason("invalidimagename"),
		},
		{
			name:     "image inspect error stays explicit",
			reason:   "ImageInspectError",
			message:  "failed to inspect image",
			expected: FailureReason("imageinspecterror"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := classifier.Classify(tt.reason, tt.message)
			if got != tt.expected {
				t.Fatalf("Classify(%q, %q) = %q, want %q", tt.reason, tt.message, got, tt.expected)
			}
		})
	}
}

func TestProcessPodPreservesSpecificReasonAcrossBackoff(t *testing.T) {
	t.Parallel()

	c := newTestCollector()
	ctx := context.Background()

	pod := &corev1.Pod{}
	pod.Namespace = "default"
	pod.Name = "demo"
	pod.Spec.NodeName = "node-1"
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{
			Name:  "app",
			Image: "docker.io/library/nginx:missing",
			State: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{
					Reason:  "ErrImagePull",
					Message: "manifest unknown",
				},
			},
		},
	}

	c.processPod(ctx, pod)

	key := pullInfoKey("default", "demo", "app")
	if got := c.failures[key].Reason; got != FailureReasonImageNotFound {
		t.Fatalf("initial failure reason = %q, want %q", got, FailureReasonImageNotFound)
	}

	pod.Status.ContainerStatuses[0].State.Waiting = &corev1.ContainerStateWaiting{
		Reason:  "ImagePullBackOff",
		Message: "Back-off pulling image \"docker.io/library/nginx:missing\"",
	}

	c.processPod(ctx, pod)

	if got := c.failures[key].Reason; got != FailureReasonImageNotFound {
		t.Fatalf("backoff should preserve prior specific reason, got %q", got)
	}
}

func TestProcessPodSkipsPrivateRegistryFailures(t *testing.T) {
	t.Parallel()

	c := newTestCollector()
	ctx := context.Background()

	pod := &corev1.Pod{}
	pod.Namespace = "default"
	pod.Name = "private"
	pod.Spec.NodeName = "node-1"
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{
			Name:  "app",
			Image: "example.internal/team/app:latest",
			State: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{
					Reason:  "ErrImagePull",
					Message: "manifest unknown",
				},
			},
		},
	}

	c.processPod(ctx, pod)

	if len(c.failures) != 0 {
		t.Fatalf("expected no tracked failures for private registry image, got %d", len(c.failures))
	}
}

func TestProcessPodTracksRealKubeletImageFailureReasons(t *testing.T) {
	t.Parallel()

	reasons := []string{"InvalidImageName", "ImageInspectError", "ErrImageNeverPull"}

	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			t.Parallel()

			c := newTestCollector()
			pod := &corev1.Pod{}
			pod.Namespace = "default"
			pod.Name = "demo"
			pod.Spec.NodeName = "node-1"
			pod.Status.ContainerStatuses = []corev1.ContainerStatus{
				{
					Name:  "app",
					Image: "docker.io/library/nginx:latest",
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  reason,
							Message: "kubelet propagated failure",
						},
					},
				},
			}

			c.processPod(context.Background(), pod)

			key := pullInfoKey("default", "demo", "app")

			info, ok := c.failures[key]
			if !ok {
				t.Fatalf("expected failure to be tracked for reason %q", reason)
			}

			if info.Reason != FailureReason(strings.ToLower(reason)) {
				t.Fatalf("tracked reason = %q, want %q", info.Reason, strings.ToLower(reason))
			}
		})
	}
}

func TestSlowPullCandidateSkipsBackoffMessages(t *testing.T) {
	t.Parallel()

	c := newTestCollector()

	cs := corev1.ContainerStatus{
		Name:  "app",
		Image: "docker.io/library/nginx:latest",
		State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{
				Reason:  "ContainerCreating",
				Message: "Back-off pulling image \"docker.io/library/nginx:latest\"",
			},
		},
	}

	if c.isSlowPullCandidate(cs) {
		t.Fatal("expected back-off pulling image message to be excluded from slow pull tracking")
	}
}

func TestCollectSlowPullExportsDurationSeconds(t *testing.T) {
	t.Parallel()

	c := newTestCollector()
	c.initMetrics("sealos")

	c.slowPulls["default/demo/app"] = &SlowPullInfo{
		Namespace: "default",
		Pod:       "demo",
		Container: "app",
		Image:     "docker.io/library/nginx:latest",
		Node:      "node-1",
		Registry:  "docker.io",
		StartedAt: time.Now().Add(-125 * time.Second),
	}

	ch := make(chan prometheus.Metric, 1)
	c.collect(ch)
	close(ch)

	metric := <-ch

	dtoMetric := &dto.Metric{}
	if err := metric.Write(dtoMetric); err != nil {
		t.Fatalf("failed to write metric: %v", err)
	}

	got := dtoMetric.GetGauge().GetValue()
	if math.Abs(got-125) > 3 {
		t.Fatalf("slow pull metric value = %v, want about 125 seconds", got)
	}
}

func newTestCollector() *Collector {
	return &Collector{
		BaseCollector: base.NewBaseCollector("imagepull", log.NewEntry(log.New())),
		config: &Config{
			SlowPullThreshold: time.Minute,
		},
		classifier: NewFailureClassifier(),
		failures:   make(map[string]*PullFailureInfo),
		slowPulls:  make(map[string]*SlowPullInfo),
		slowTimers: make(map[string]*time.Timer),
		logger:     log.NewEntry(log.New()),
	}
}
