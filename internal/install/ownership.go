package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/adapters"
	"github.com/zigai/aht/v2/pkg/registry"
)

const (
	ArtifactMissing  ArtifactStatus = "missing"
	ArtifactPartial  ArtifactStatus = "partial"
	ArtifactDisabled ArtifactStatus = "disabled"
	ArtifactCurrent  ArtifactStatus = "current"
	ArtifactStale    ArtifactStatus = "stale"
	ArtifactForeign  ArtifactStatus = "foreign"
)

type ArtifactStatus string

// classifyArtifact inspects an artifact generated for harnessID without
// modifying it. A directory is classified by its marker file. Content without
// the managed marker, or that records another integration, is foreign.
func classifyArtifact(path string, harnessID registry.Harness) (ArtifactStatus, error) {
	return classifyArtifactWith(path, func(content string) ArtifactStatus {
		return classifyOwnedArtifactContent(content, harnessID)
	})
}

func classifyArtifactWith(
	path string,
	classify func(string) ArtifactStatus,
) (ArtifactStatus, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ArtifactMissing, nil
		}

		return "", fmt.Errorf("checking artifact %s: %w", path, err)
	}

	contentPath := path
	if info.IsDir() {
		contentPath = filepath.Join(path, harness.ArtifactMarkerFile)
	}

	data, err := os.ReadFile(contentPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ArtifactForeign, nil
		}

		return "", fmt.Errorf("reading artifact %s: %w", contentPath, err)
	}

	return classify(string(data)), nil
}

func classifyExpectedIdentity(content string, expected harness.ArtifactIdentity) ArtifactStatus {
	identity, managed := harness.ParseArtifactIdentity(content)
	if !managed || identity.ID != expected.ID {
		return ArtifactForeign
	}

	if identity.Version != expected.Version {
		return ArtifactStale
	}

	return ArtifactCurrent
}

func classifyOwnedArtifactContent(content string, harnessID registry.Harness) ArtifactStatus {
	adapter, ok := adapters.Find(harnessID)
	if !ok {
		return ArtifactForeign
	}

	identity, managed := harness.ParseArtifactIdentity(content)
	if !managed || !ownedBy(identity, adapter.Definition()) {
		return ArtifactForeign
	}

	return versionStatus(identity.Version, adapter.Definition())
}

// classifyArtifactContent classifies a shared config file, which identifies
// generated hooks by the managed marker or, when it cannot hold the marker, by
// their reporter source. Every generated hook for harnessID must be current.
func classifyArtifactContent(content string, harnessID registry.Harness) ArtifactStatus {
	adapter, ok := adapters.Find(harnessID)
	if !ok {
		return ArtifactForeign
	}

	definition := adapter.Definition()

	var versions []int
	if identity, managed := harness.ParseArtifactIdentity(content); managed &&
		ownedBy(identity, definition) {
		versions = append(versions, identity.Version)
	}

	for _, reporter := range harness.ParseReporterIdentities(content) {
		if reporter.Source == definition.IntegrationSource {
			versions = append(versions, reporter.Version)
		}
	}

	if len(versions) == 0 {
		return ArtifactForeign
	}

	for _, version := range versions {
		if versionStatus(version, definition) == ArtifactStale {
			return ArtifactStale
		}
	}

	return ArtifactCurrent
}

func ownedBy(identity harness.ArtifactIdentity, definition harness.Definition) bool {
	if identity.ID != "" {
		owner, err := adapters.Parse(identity.ID)
		if err != nil || owner != definition.ID {
			return false
		}
	}

	return identity.Source == "" || identity.Source == definition.IntegrationSource
}

func versionStatus(version int, definition harness.Definition) ArtifactStatus {
	if version != definition.IntegrationVersion {
		return ArtifactStale
	}

	return ArtifactCurrent
}
