package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	harnesspkg "github.com/zigai/aht/internal/harness"
	harnesscatalog "github.com/zigai/aht/internal/harness/catalog"
	"github.com/zigai/aht/pkg/registry"
)

const (
	ArtifactMissing ArtifactStatus = "missing"
	ArtifactCurrent ArtifactStatus = "current"
	ArtifactStale   ArtifactStatus = "stale"
	ArtifactForeign ArtifactStatus = "foreign"

	managedIntegrationVersion = harnesspkg.IntegrationVersion
	integrationCaptureGroups  = 2
)

var (
	integrationVersionPattern = regexp.MustCompile(`(?i)(?:aht[_-]?integration[_-]?version\s*[=:]|--reporter-version)\s*["']?([0-9]+)`)
	integrationSourcePattern  = regexp.MustCompile(`(?i)(?:aht[_-]?integration\s*[=:]|--reporter\s)`)
	integrationIDPattern      = regexp.MustCompile(`(?i)AHT_INTEGRATION_ID\s*=\s*["']?([a-z0-9_-]+)`)
)

// ArtifactStatus describes whether a managed integration artifact is absent,
// up to date, an older managed generation, or owned by somebody else.
type ArtifactStatus string

func (s ArtifactStatus) IsValid() bool {
	switch s {
	case ArtifactMissing, ArtifactCurrent, ArtifactStale, ArtifactForeign:
		return true
	}
	return false
}

// ClassifyArtifact inspects a generated artifact without modifying it. It is
// intentionally format-agnostic: managed ownership is established by the
// marker or source metadata and generation is established by the version.
func ClassifyArtifact(path string) (ArtifactStatus, error) {
	status, _, err := classifyArtifactWithID(path)
	return status, err
}

func classifyArtifactWithID(path string) (ArtifactStatus, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ArtifactMissing, "", nil
		}
		return "", "", fmt.Errorf("checking artifact %s: %w", path, err)
	}
	contentPath := path
	if info.IsDir() {
		contentPath = filepath.Join(path, ".aht-managed")
	}
	data, err := os.ReadFile(contentPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ArtifactForeign, "", nil
		}
		return "", "", fmt.Errorf("reading artifact %s: %w", contentPath, err)
	}
	content := string(data)
	return classifyArtifactContent(content), integrationIDFromContent(content), nil
}

func classifyArtifactContent(content string) ArtifactStatus {
	if !strings.Contains(content, managedMarker) && !integrationSourcePattern.MatchString(content) {
		return ArtifactForeign
	}

	match := integrationVersionPattern.FindStringSubmatch(content)
	if len(match) != integrationCaptureGroups {
		return ArtifactStale
	}
	version, err := strconv.Atoi(match[1])
	if err != nil || version != expectedIntegrationVersion(content) {
		return ArtifactStale
	}

	return ArtifactCurrent
}

func expectedIntegrationVersion(content string) int {
	match := integrationIDPattern.FindStringSubmatch(content)
	if len(match) != integrationCaptureGroups {
		return managedIntegrationVersion
	}
	id, err := harnesscatalog.Normalize(match[1])
	if err != nil {
		return managedIntegrationVersion
	}
	return harnesscatalog.IntegrationVersionFor(id)
}

func integrationIDFromContent(content string) string {
	match := integrationIDPattern.FindStringSubmatch(content)
	if len(match) != integrationCaptureGroups {
		return ""
	}
	id, err := harnesscatalog.Normalize(match[1])
	if err != nil {
		return strings.ToLower(match[1])
	}
	return string(id)
}

func classifyArtifactForHarness(path string, harnessID registry.Harness) (ArtifactStatus, error) {
	status, integrationID, err := classifyArtifactWithID(path)
	if err != nil || status == ArtifactMissing || status == ArtifactForeign {
		return status, err
	}
	if integrationID != "" && integrationID != string(harnessID) {
		return ArtifactForeign, nil
	}
	return status, nil
}
