package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	harnesspkg "github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/harness/catalog"
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

// ArtifactStatus describes whether a managed integration artifact is absent,
// incomplete, disabled, up to date, stale, or owned by somebody else.
type ArtifactStatus string

// classifyArtifact inspects an artifact generated for harnessID without
// modifying it. A directory is classified by its marker file. Content that
// records another integration is foreign.
func classifyArtifact(path string, harnessID registry.Harness) (ArtifactStatus, error) {
	return classifyArtifactWith(path, func(content string) ArtifactStatus {
		return classifyArtifactContent(content, harnessID)
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
		contentPath = filepath.Join(path, harnesspkg.ArtifactMarkerFile)
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

func classifyExpectedIdentity(content string, expected harnesspkg.ArtifactIdentity) ArtifactStatus {
	identity, managed := harnesspkg.ParseArtifactIdentity(content)
	if !managed || identity.ID != expected.ID {
		return ArtifactForeign
	}

	if identity.Version != expected.Version {
		return ArtifactStale
	}

	return ArtifactCurrent
}

func classifyArtifactContent(content string, harnessID registry.Harness) ArtifactStatus {
	identity, managed := harnesspkg.ParseArtifactIdentity(content)
	if !managed {
		return ArtifactForeign
	}

	if identity.ID != "" {
		owner, err := catalog.Parse(identity.ID)
		if err != nil || owner != harnessID {
			return ArtifactForeign
		}
	}

	if identity.Version != catalog.IntegrationVersionFor(harnessID) {
		return ArtifactStale
	}

	return ArtifactCurrent
}
