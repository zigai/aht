package harness

import (
	"regexp"
	"strconv"
	"strings"
)

// ArtifactMarkerFile is the file that marks a managed plugin directory.
const ArtifactMarkerFile = ".aht-managed"

const artifactCaptureGroups = 2

var (
	artifactVersionPattern = regexp.MustCompile(
		`(?i)(?:aht[_-]?integration[_-]?version\s*[=:]|--reporter-version)\s*["']?([0-9]+)`,
	)
	artifactSourcePattern = regexp.MustCompile(
		`(?i)(?:aht[_-]?integration\s*[=:]|--reporter\s)`,
	)
	artifactIDPattern = regexp.MustCompile(
		`(?i)AHT_INTEGRATION_ID\s*=\s*["']?([a-z0-9_-]+)`,
	)
	artifactSourceNamePattern = regexp.MustCompile(
		`(?i)AHT_SOURCE\s*=\s*["']?([a-z0-9_-]+)`,
	)
)

// ArtifactIdentity names the integration that generated a managed artifact.
// Empty fields mean the artifact does not record that value.
type ArtifactIdentity struct {
	ID      string
	Version int
	Source  string
}

// Identity is the identity of artifacts generated for this definition.
func (definition Definition) Identity() ArtifactIdentity {
	return ArtifactIdentity{
		ID:      string(definition.ID),
		Version: definition.IntegrationVersion,
		Source:  definition.IntegrationSource,
	}
}

// Marker renders the identity as marker lines, each starting with prefix.
func (identity ArtifactIdentity) Marker(prefix string) string {
	lines := []string{
		ManagedMarker,
		"AHT_INTEGRATION_ID=" + identity.ID,
		"AHT_INTEGRATION_VERSION=" + strconv.Itoa(identity.Version),
		"AHT_SOURCE=" + identity.Source,
	}

	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(prefix + line + "\n")
	}

	return builder.String()
}

// ParseArtifactIdentity reads the identity recorded in generated content. It
// reports false when the content carries neither the managed marker nor
// reporter metadata.
func ParseArtifactIdentity(content string) (ArtifactIdentity, bool) {
	if !strings.Contains(content, ManagedMarker) && !artifactSourcePattern.MatchString(content) {
		return ArtifactIdentity{}, false
	}

	var identity ArtifactIdentity
	if match := artifactIDPattern.FindStringSubmatch(content); len(match) == artifactCaptureGroups {
		identity.ID = strings.ToLower(match[1])
	}

	if match := artifactVersionPattern.FindStringSubmatch(content); len(
		match,
	) == artifactCaptureGroups {
		if version, err := strconv.Atoi(match[1]); err == nil {
			identity.Version = version
		}
	}

	if match := artifactSourceNamePattern.FindStringSubmatch(content); len(
		match,
	) == artifactCaptureGroups {
		identity.Source = match[1]
	}

	return identity, true
}
