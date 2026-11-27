package harness

import (
	"cmp"
	"regexp"
	"strconv"
	"strings"
)

const ArtifactMarkerFile = ".aht-managed"

const artifactCaptureGroups = 2

var (
	artifactVersionPattern = regexp.MustCompile(
		`(?i)AHT_INTEGRATION_VERSION\s*=\s*["']?([0-9]+)`,
	)
	artifactIDPattern = regexp.MustCompile(
		`(?i)AHT_INTEGRATION_ID\s*=\s*["']?([a-z0-9_-]+)`,
	)
	artifactSourceNamePattern = regexp.MustCompile(
		`(?i)AHT_SOURCE\s*=\s*["']?([a-z0-9_-]+)`,
	)
	// Report commands carry --reporter-version before --reporter, and legacy
	// commands carry aht_integration_version before aht_integration.
	reporterMetadataPattern = regexp.MustCompile(
		`(?i)(?:(--reporter-version|--reporter)(?:=|\s+)|` +
			`(aht[_-]?integration[_-]?version|aht[_-]?integration)\s*[=:]\s*)["']?([a-z0-9_.-]+)`,
	)
)

// ArtifactIdentity names the integration that generated a managed artifact.
// Empty fields mean the artifact does not record that value.
type ArtifactIdentity struct {
	ID      string
	Version int
	Source  string
}

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

// ParseArtifactIdentity reads the identity recorded in content that carries
// ManagedMarker. Without marker lines for the version or source, it uses the
// first report command in the content. It reports false when the content lacks
// ManagedMarker.
func ParseArtifactIdentity(content string) (ArtifactIdentity, bool) {
	if !strings.Contains(content, ManagedMarker) {
		return ArtifactIdentity{}, false
	}

	var identity ArtifactIdentity
	if match := artifactIDPattern.FindStringSubmatch(content); len(match) == artifactCaptureGroups {
		identity.ID = strings.ToLower(match[1])
	}

	if match := artifactVersionPattern.FindStringSubmatch(content); len(
		match,
	) == artifactCaptureGroups {
		identity.Version = artifactVersion(match[1])
	}

	if match := artifactSourceNamePattern.FindStringSubmatch(content); len(
		match,
	) == artifactCaptureGroups {
		identity.Source = match[1]
	}

	if reporters := ParseReporterIdentities(content); len(reporters) > 0 {
		identity.Version = cmp.Or(identity.Version, reporters[0].Version)
		identity.Source = cmp.Or(identity.Source, reporters[0].Source)
	}

	return identity, true
}

// ParseReporterIdentities returns the source and version of each report
// command in content that names its reporter, in order. Version is zero for a
// command without a reporter version.
func ParseReporterIdentities(content string) []ArtifactIdentity {
	var (
		reporters []ArtifactIdentity
		version   int
	)

	for _, match := range reporterMetadataPattern.FindAllStringSubmatch(content, -1) {
		key := strings.ToLower(match[1] + match[2])
		if strings.HasSuffix(key, "version") {
			version = artifactVersion(match[3])
			continue
		}

		reporters = append(reporters, ArtifactIdentity{
			ID:      "",
			Version: version,
			Source:  strings.ToLower(match[3]),
		})
		version = 0
	}

	return reporters
}

func artifactVersion(value string) int {
	version, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}

	return version
}
