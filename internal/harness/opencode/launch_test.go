package opencode

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/zigai/aht/v2/internal/harness"
)

func TestLaunchDisablesAuxiliaryTitleRequests(t *testing.T) {
	root := t.TempDir()
	model := harness.LaunchModel{Endpoint: "http://127.0.0.1:1234", APIKey: "fake", Model: "fake"}
	plan, err := New().LaunchPlan(harness.LaunchPaths{Root: root}, model)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "opencode", "opencode.json")
	for _, file := range plan.Files {
		if file.Path != configPath {
			continue
		}
		// Recreating a scratch launch must replace an old configuration too.
		for _, previous := range [][]byte{nil, []byte(`{"agents":{"title":{"disabled":false}}}`)} {
			data, err := file.Update(previous)
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Model  string `json:"model"`
				Agents map[string]struct {
					Disabled bool `json:"disabled"`
				} `json:"agents"`
			}
			if err := json.Unmarshal(data, &config); err != nil {
				t.Fatal(err)
			}
			if len(config.Agents) != 1 || !config.Agents["title"].Disabled {
				t.Fatalf("scratch launch must disable only the auxiliary title agent: %s", data)
			}
			if config.Model != harness.LaunchProviderName+"/fake" {
				t.Fatalf("primary model changed: %s", data)
			}
		}
		return
	}
	t.Fatal("launch plan has no native config")
}
