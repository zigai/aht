//go:build integration

package systemtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallRecipeUsesInstalledBinary(t *testing.T) {
	just, err := exec.LookPath("just")
	if err != nil {
		t.Skip("just is required for the install recipe test")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, recipe                       string
		useGOBIN, failInstall, failUpgrade bool
	}{
		{name: "explicit GOBIN", recipe: "install", useGOBIN: true},
		{name: "first GOPATH entry", recipe: "install"},
		{name: "binary only", recipe: "install-binary", useGOBIN: true},
		{name: "install failure", recipe: "install", useGOBIN: true, failInstall: true},
		{name: "upgrade failure", recipe: "install", useGOBIN: true, failUpgrade: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "tools")
			installed := filepath.Join(dir, "installed binary")
			gobin := installed
			if !test.useGOBIN {
				gobin = ""
				installed = filepath.Join(dir, "first go path", "bin")
			}
			writeRecipeExecutable(t, filepath.Join(bin, "go"), `#!/bin/sh
set -eu
case "$*" in
 "install .")
  if [ "$AHT_TEST_FAIL_INSTALL" = yes ]; then exit 7; fi
  mkdir -p "$AHT_TEST_INSTALL_DIR"
  cp "$AHT_TEST_NEW_BINARY" "$AHT_TEST_INSTALL_DIR/aht"
  ;;
 "env GOBIN") printf '%s\n' "$AHT_TEST_GOBIN" ;;
 "env GOPATH") printf '%s\n' "$AHT_TEST_GOPATH" ;;
 *) exit 9 ;;
esac
`)
			writeRecipeExecutable(t, filepath.Join(bin, "aht"), "#!/bin/sh\nexit 99\n")
			template := filepath.Join(dir, "new-aht")
			writeRecipeExecutable(t, template, `#!/bin/sh
set -eu
printf '%s\n' "$*" > "$AHT_TEST_UPGRADE_CALLS"
if [ "$AHT_TEST_FAIL_UPGRADE" = yes ]; then exit 8; fi
`)
			calls := filepath.Join(dir, "calls")
			command := exec.CommandContext(t.Context(), just, "--justfile", filepath.Join(root, "Justfile"), test.recipe)
			command.Env = append(os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"AHT_TEST_INSTALL_DIR="+installed,
				"AHT_TEST_NEW_BINARY="+template,
				"AHT_TEST_GOBIN="+gobin,
				"AHT_TEST_GOPATH="+filepath.Join(dir, "first go path")+string(os.PathListSeparator)+filepath.Join(dir, "second"),
				"AHT_TEST_UPGRADE_CALLS="+calls,
				"AHT_TEST_FAIL_INSTALL="+recipeBool(test.failInstall),
				"AHT_TEST_FAIL_UPGRADE="+recipeBool(test.failUpgrade),
			)
			output, err := command.CombinedOutput()
			if (err != nil) != (test.failInstall || test.failUpgrade) {
				t.Fatalf("recipe = %v\n%s", err, output)
			}
			assertRecipeUpgrade(t, calls, test.recipe == "install" && !test.failInstall)
		})
	}
}

func recipeBool(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func writeRecipeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func assertRecipeUpgrade(t *testing.T, path string, expected bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if !expected {
		if !os.IsNotExist(err) {
			t.Fatalf("unexpected upgrade: %s, %v", data, err)
		}
		return
	}
	if err != nil || strings.TrimSpace(string(data)) != "manage upgrade" {
		t.Fatalf("upgrade invocation = %q, %v", data, err)
	}
}
