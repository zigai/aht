package agy

import (
	"context"
	"slices"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
)

type importRegistration struct {
	Path       string
	Name       string
	Source     string
	Components []string
}

func (registration importRegistration) ID() string { return registration.Name }

func (registration importRegistration) Label() string { return registration.Path }

func (registration importRegistration) Inspect(
	_ context.Context,
	_ string,
) (harness.PluginRegistrationState, error) {
	manifest, err := readImportManifest(registration.Path)
	if err != nil {
		return harness.PluginRegistrationMissing, err
	}

	for _, entry := range manifest.Imports {
		if entry.name() != registration.Name {
			continue
		}

		if entry.source() != registration.Source || entry.importedAt() == "" {
			return harness.PluginRegistrationStale, nil
		}

		for _, component := range registration.Components {
			if !slices.Contains(entry.components(), component) {
				return harness.PluginRegistrationStale, nil
			}
		}

		return harness.PluginRegistrationCurrent, nil
	}

	return harness.PluginRegistrationMissing, nil
}

func (importRegistration) EnsureMutable(string) error { return nil }

func (registration importRegistration) Install(_ context.Context, _ string) error {
	manifest, err := readImportManifest(registration.Path)
	if err != nil {
		return err
	}

	next, changed := upsertImport(manifest, registration, time.Now().UTC())
	if !changed {
		return nil
	}

	return writeImportManifest(registration.Path, next)
}

func (importRegistration) CleanupFailedInstall(
	context.Context,
	harness.PluginRegistrationState,
	string,
) error {
	return nil
}

func (registration importRegistration) Remove(_ context.Context, _ string) error {
	manifest, err := readImportManifest(registration.Path)
	if err != nil {
		return err
	}

	next, changed := removeImport(manifest, registration.Name)
	if !changed {
		return nil
	}

	return writeImportManifest(registration.Path, next)
}
