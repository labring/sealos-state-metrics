package collector_test

import (
	"errors"
	"testing"

	"github.com/labring/sealos-state-metrics/pkg/collector"
	"github.com/labring/sealos-state-metrics/pkg/collector/imagepull"
	"github.com/labring/sealos-state-metrics/pkg/collector/kubeblocks"
	"github.com/labring/sealos-state-metrics/pkg/collector/node"
	"github.com/labring/sealos-state-metrics/pkg/collector/zombie"
	"github.com/labring/sealos-state-metrics/pkg/config"
	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// mockFailingClientProvider always returns an error
type mockFailingClientProvider struct {
	err error
}

func (m *mockFailingClientProvider) GetClient() (kubernetes.Interface, error) {
	return nil, m.err
}

func (m *mockFailingClientProvider) GetRestConfig() (*rest.Config, error) {
	return nil, m.err
}

// TestErrorWrapping verifies that collector factories properly wrap errors with %w
func TestErrorWrapping(t *testing.T) {
	// Silence logger for cleaner test output
	log.SetLevel(log.ErrorLevel)
	defer log.SetLevel(log.InfoLevel)

	baseError := errors.New("mock kubernetes initialization error")

	testCases := []struct {
		name    string
		factory collector.Factory
		wantErr string
	}{
		{
			name:    "node collector wraps client error",
			factory: node.NewCollector,
			wantErr: "kubernetes client is required but not available",
		},
		{
			name:    "imagepull collector wraps client error",
			factory: imagepull.NewCollector,
			wantErr: "kubernetes client is required but not available",
		},
		{
			name:    "zombie collector wraps client error",
			factory: zombie.NewCollector,
			wantErr: "kubernetes client is required but not available",
		},
		{
			name:    "kubeblocks collector wraps rest config error",
			factory: kubeblocks.NewCollector,
			wantErr: "kubernetes rest config is required but not available",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			factoryCtx := &collector.FactoryContext{
				ClientProvider:   &mockFailingClientProvider{err: baseError},
				ConfigLoader:     config.NewEnvConfigLoader(),
				MetricsNamespace: "test",
				Logger:           log.WithField("test", tc.name),
			}

			_, err := tc.factory(factoryCtx)
			if err == nil {
				t.Fatal("Expected error but got nil")
			}

			// Verify error message contains expected text
			if !errors.Is(err, baseError) {
				t.Errorf(
					"Error should wrap base error\nGot: %v\nWant to unwrap to: %v",
					err,
					baseError,
				)
			}

			// Verify error message
			errMsg := err.Error()
			if len(errMsg) < len(tc.wantErr) || errMsg[:len(tc.wantErr)] != tc.wantErr {
				t.Errorf("Error message should start with %q\nGot: %v", tc.wantErr, errMsg)
			}
		})
	}
}
