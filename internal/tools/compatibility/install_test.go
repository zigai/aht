package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallationsPinTheSelectedRelease(t *testing.T) {
	for _, spec := range catalog() {
		if spec.Source != "weekly" {
			t.Run(spec.ID, func(t *testing.T) { assertVersionPin(t, spec) })
		}
	}
}

func assertVersionPin(t *testing.T, spec harnessSpec) {
	t.Helper()
	version := "1.2.3"
	if spec.ID == "goose" {
		version = "v1.2.3"
	}
	text := installationText(t, spec, version, t.TempDir(), t.TempDir())
	if strings.Contains(text, "latest") {
		t.Error("resolves latest during install")
	}
	expected := version
	switch spec.Source {
	case "npm":
		expected = spec.Package + "@" + version
	case "pypi":
		expected = spec.Package + "==" + version
		if spec.ID == "hermes" {
			expected = spec.Package + "[acp]==" + version
		}
	case "github":
		expected = "/releases/download/" + version + "/" + spec.Asset
	}
	if !strings.Contains(text, expected) {
		t.Errorf("lost exact version: %s", text)
	}
}

func TestNativeReleaseInstallations(t *testing.T) {
	work, bin := t.TempDir(), t.TempDir()
	goose := installationText(t, testHarness(t, "goose"), "v1.2.3", work, bin)
	if !strings.Contains(goose, "GOOSE_VERSION=v1.2.3") || !strings.Contains(goose, "CONFIGURE=false") {
		t.Error("goose installer is not pinned/noninteractive")
	}
	agy := installationText(t, testHarness(t, "agy"), "1.2.3", work, bin)
	if !strings.Contains(agy, "antigravity") || !strings.Contains(agy, filepath.Join(bin, "agy")) {
		t.Error("agy release binary was not mapped to its CLI name")
	}
}

func installationText(t *testing.T, spec harnessSpec, version, work, bin string) string {
	t.Helper()
	commands, err := installationCommands(spec, version, work, bin)
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, command := range commands {
		parts = append(parts, command.Args...)
		parts = append(parts, command.Env...)
	}
	return strings.Join(parts, "\n")
}

func TestInvalidInstallVersionRunsNothing(t *testing.T) {
	for _, id := range []string{"droid", "grok", "cursor"} {
		spec := testHarness(t, id)
		for _, version := range []string{"latest", "1.2.3; echo wrong"} {
			if commands, err := installationCommands(spec, version, t.TempDir(), t.TempDir()); err == nil || len(commands) != 0 {
				t.Fatalf("accepted %s %q", id, version)
			}
		}
	}
}
