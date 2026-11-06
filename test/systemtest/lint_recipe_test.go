//go:build integration

package systemtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLintRecipesPreserveBuildFlagsAndIsolateCache(t *testing.T) {
	just, err := exec.LookPath("just")
	if err != nil {
		t.Skip("just is required for the lint recipe test")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name, flags, cache, xdg, wantCache string
	}{
		{name: "home default", wantCache: "home with space/.cache/golangci-lint/source-positions"},
		{
			name:      "xdg default",
			flags:     "-p=9 -trimpath",
			xdg:       "xdg cache",
			wantCache: "xdg cache/golangci-lint/source-positions",
		},
		{
			name:      "explicit cache",
			flags:     "-p=9 -trimpath -mod=readonly",
			cache:     "explicit cache",
			xdg:       "ignored cache",
			wantCache: "explicit cache/source-positions",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, recipe := range []string{"lint", "fix", "format", "formatcheck", "source-checks"} {
				t.Run(recipe, func(t *testing.T) {
					dir := t.TempDir()

					bin := filepath.Join(dir, "tools")
					for _, tool := range []string{"go", "git", "shfmt", "rumdl"} {
						writeRecipeExecutable(t, filepath.Join(bin, tool), "#!/bin/sh\nexit 0\n")
					}

					probe := filepath.Join(bin, "lint-probe")
					writeRecipeExecutable(t, probe, `#!/bin/sh
set -eu
printf 'flags=%s\ncache=%s\n\n' "$GOFLAGS" "$GOLANGCI_LINT_CACHE" >> "$AHT_TEST_LINT_CALLS"
`)

					calls := filepath.Join(dir, "calls")
					command := exec.CommandContext(
						t.Context(),
						just,
						"--justfile",
						filepath.Join(root, "Justfile"),
						recipe,
					)

					cache, xdg := "", ""
					if test.cache != "" {
						cache = filepath.Join(dir, test.cache)
					}

					if test.xdg != "" {
						xdg = filepath.Join(dir, test.xdg)
					}

					command.Env = append(os.Environ(),
						"HOME="+filepath.Join(dir, "home with space"),
						"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
						"AHT_TEST_LINT_CALLS="+calls,
						"GOFLAGS="+test.flags,
						"GOLANGCI_LINT_CACHE="+cache,
						"XDG_CACHE_HOME="+xdg,
						"AHT_GOLANGCI_LINT="+probe,
					)

					output, err := command.CombinedOutput()
					if err != nil {
						t.Fatalf("recipe = %v\n%s", err, output)
					}

					data, err := os.ReadFile(calls)
					if err != nil {
						t.Fatal(err)
					}

					want := "flags=" + test.flags + " -trimpath=false\n" +
						"cache=" + filepath.Join(dir, test.wantCache)
					for call := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n\n"), "\n\n") {
						if call != want {
							t.Fatalf("lint environment = %q, want %q", call, want)
						}
					}
				})
			}
		})
	}
}
