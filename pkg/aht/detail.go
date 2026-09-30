package aht

import "github.com/zigai/aht/v2/pkg/registry"

const (
	DetailPermission  = registry.DetailPermission
	DetailQuestion    = registry.DetailQuestion
	DetailGeneral     = registry.DetailGeneral
	DetailUsageLimit  = registry.DetailUsageLimit
	DetailCurrent     = registry.DetailCurrent
	DetailStale       = registry.DetailStale
	DetailMissing     = registry.DetailMissing
	DetailUnsupported = registry.DetailUnsupported
)

type (
	ActivityDetail     = registry.ActivityDetail
	DetailEvidence     = registry.DetailEvidence
	StateDetail        = registry.StateDetail
	DetailQuality      = registry.DetailQuality
	DetailSupport      = registry.DetailSupport
	DetailCapabilities = registry.DetailCapabilities
)
