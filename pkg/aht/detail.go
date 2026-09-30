package aht

import "github.com/zigai/aht/v2/pkg/registry"

const (
	ActivityDetailPermission = registry.ActivityDetailPermission
	ActivityDetailQuestion   = registry.ActivityDetailQuestion
	ActivityDetailGeneral    = registry.ActivityDetailGeneral
	ActivityDetailUsageLimit = registry.ActivityDetailUsageLimit
	DetailQualityCurrent     = registry.DetailQualityCurrent
	DetailQualityStale       = registry.DetailQualityStale
	DetailQualityMissing     = registry.DetailQualityMissing
	DetailQualityUnsupported = registry.DetailQualityUnsupported
)

type (
	ActivityDetail     = registry.ActivityDetail
	DetailEvidence     = registry.DetailEvidence
	StateDetail        = registry.StateDetail
	DetailQuality      = registry.DetailQuality
	DetailSupport      = registry.DetailSupport
	DetailCapabilities = registry.DetailCapabilities
)
