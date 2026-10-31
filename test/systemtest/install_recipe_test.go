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
		name, recipe             string
		failInstall, failUpgrade bool
	}{
		{name: "install", recipe: "install"},
		{name: "binary only", recipe: "install-binary"},
		{name: "install failure", recipe: "install", failInstall: true},
		{name: "upgrade failure", recipe: "install", failUpgrade: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "tools")
			writeRecipeExecutable(t, filepath.Join(bin, "go"), `#!/bin/sh
set -eu
case "$1" in
 install)
  if [ "$AHT_TEST_FAIL_INSTALL" = yes ]; then exit 7; fi
  mkdir -p "$GOBIN"
  cp "$AHT_TEST_NEW_BINARY" "$GOBIN/aht"
  ;;
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
			command := exec.CommandContext(
				t.Context(),
				just,
				"--justfile",
				filepath.Join(root, "Justfile"),
				test.recipe,
			)
			command.Env = append(os.Environ(),
				"HOME="+filepath.Join(dir, "home with space"),
				"GOBIN=",
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"AHT_TEST_NEW_BINARY="+template,
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

func writeRecipeExecutable(t *testing.T, path string, content string) {
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
