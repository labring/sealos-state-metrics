// Package all imports all collector packages to register their factories
package all

import (
	// Import all collectors to trigger their init() functions
	_ "github.com/labring/sealos-state-metrics/pkg/collector/cloudbalance"
	_ "github.com/labring/sealos-state-metrics/pkg/collector/domain"
	_ "github.com/labring/sealos-state-metrics/pkg/collector/dynamic"
	_ "github.com/labring/sealos-state-metrics/pkg/collector/imagepull"
	_ "github.com/labring/sealos-state-metrics/pkg/collector/kubeblocks"
	_ "github.com/labring/sealos-state-metrics/pkg/collector/lvm"
	_ "github.com/labring/sealos-state-metrics/pkg/collector/node"
	_ "github.com/labring/sealos-state-metrics/pkg/collector/zombie"
)
