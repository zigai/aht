package prime_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/pkg/history"
	"github.com/zigai/aht/v2/pkg/registry"
)

func writeTranscript(t *testing.T, path, id, prompt string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session","id":"` + id + `","cwd":"/work"}` + "\n" +
		`{"type":"message","id":"u1","message":{"role":"user","content":"` + prompt + `"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultPrimeHistoryFindsRootAndRLMChildrenOnce(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, ".prime", "agent")
	rootFile := filepath.Join(agentDir, "sessions", "root.jsonl")
	childFile := filepath.Join(agentDir, "session-artifacts", "root", "sub-child", "child.jsonl")
	writeTranscript(t, rootFile, "root", "unique-root-term")
	writeTranscript(t, childFile, "child", "unique-child-term")
	artifactLog := filepath.Join(agentDir, "session-artifacts", "root", "semantic-edges.jsonl")
	if err := os.WriteFile(artifactLog, []byte(`{"type":"session_registered","session_id":"root"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", agentDir)
	// A worker receives its own child directory, not the daemon's shared sessions directory.
	t.Setenv("PRIME_AGENT_SESSION_DIR", filepath.Dir(childFile))

	sources, err := history.DefaultSources()
	if err != nil {
		t.Fatal(err)
	}
	var primePaths []string
	for _, source := range sources {
		if source.Harness == registry.Harness("prime-agent") {
			primePaths = append(primePaths, source.Path)
		}
	}
	if len(primePaths) != 2 || primePaths[0] != filepath.Dir(rootFile) || primePaths[1] != filepath.Join(agentDir, "session-artifacts") {
		t.Fatalf("Prime default source paths = %q", primePaths)
	}
	catalog := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	for _, term := range []string{"unique-root-term", "unique-child-term"} {
		result, err := catalog.Search(t.Context(), history.Query{Harnesses: []registry.Harness{registry.Harness("prime-agent")}, Terms: []string{term}})
		if err != nil || len(result.Issues) != 0 || len(result.Matches) != 1 {
			t.Fatalf("search %q = matches %#v, issues %#v, err %v", term, result.Matches, result.Issues, err)
		}
	}
}

func TestDefaultPrimeHistoryHonorsExternalSessionOverride(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, ".prime", "agent")
	custom := filepath.Join(home, "custom-sessions")
	writeTranscript(t, filepath.Join(custom, "custom.jsonl"), "custom", "unique-external-term")
	writeTranscript(t, filepath.Join(agentDir, "session-artifacts", "custom", "sub-child", "child.jsonl"), "child", "unique-child-term")
	t.Setenv("HOME", home)
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", agentDir)
	t.Setenv("PRIME_AGENT_SESSION_DIR", custom)
	sources, err := history.DefaultSources()
	if err != nil {
		t.Fatal(err)
	}
	var primePaths []string
	for _, source := range sources {
		if source.Harness == registry.Harness("prime-agent") {
			primePaths = append(primePaths, source.Path)
		}
	}
	if len(primePaths) != 2 || primePaths[0] != custom || primePaths[1] != filepath.Join(agentDir, "session-artifacts") {
		t.Fatalf("Prime overridden source paths = %q", primePaths)
	}
	catalog := history.Catalog{IndexPath: filepath.Join(t.TempDir(), "index.sqlite")}
	for _, term := range []string{"unique-external-term", "unique-child-term"} {
		result, err := catalog.Search(t.Context(), history.Query{Harnesses: []registry.Harness{registry.Harness("prime-agent")}, Terms: []string{term}})
		if err != nil || len(result.Matches) != 1 {
			t.Fatalf("search %q = matches %#v, issues %#v, err %v", term, result.Matches, result.Issues, err)
		}
	}
}
