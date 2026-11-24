package launch

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zigai/aht/v2/pkg/registry"
)

func TestAgyPlan(t *testing.T) {
	t.Parallel()

	if !Supported(registry.Harness("agy")) {
		t.Fatal("agy is not registered in launch plans")
	}

	paths := Paths{
		Root: "/tmp/root",
		Home: "/tmp/root/home",
		Work: "/tmp/root/work",
	}
	model := Model{
		Endpoint: "http://127.0.0.1:4321",
		Model:    "fake",
		APIKey:   "secret-key",
	}

	plan, err := agyPlan(paths, model)
	if err != nil {
		t.Fatal(err)
	}

	if plan.Executable != "agy" || plan.Protocol != ProtocolGemini {
		t.Fatalf("executable=%q protocol=%q, want agy / gemini", plan.Executable, plan.Protocol)
	}

	assertEnvironment(t, plan.Env, map[string]string{
		"GEMINI_API_KEY":         "secret-key",
		"GOOGLE_GEMINI_BASE_URL": "http://127.0.0.1:4321",
	})

	if len(plan.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(plan.Files))
	}

	wantSettingsPath := filepath.Join(paths.Home, ".gemini", "antigravity-cli", "settings.json")
	if plan.Files[0].Path != wantSettingsPath {
		t.Fatalf("settings path = %q, want %q", plan.Files[0].Path, wantSettingsPath)
	}

	existing := []byte(
		`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"aht"}]}]},` +
			`"trustedWorkspaces":["/other"]}`,
	)

	updated, err := plan.Files[0].Update(existing)
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(updated, &decoded); err != nil {
		t.Fatalf("decode updated settings: %v", err)
	}

	if decoded["modelProvider"] != "gemini" ||
		decoded["onboardingComplete"] != true ||
		decoded["consumerOnboardingComplete"] != true {
		t.Fatalf("settings = %#v, want gemini provider and completed onboarding", decoded)
	}

	if decoded["hooks"] == nil {
		t.Fatalf("existing hooks were dropped from settings: %#v", decoded)
	}

	trusted, ok := decoded["trustedWorkspaces"].([]any)
	if !ok || len(trusted) != 2 || trusted[0] != "/other" || trusted[1] != paths.Work {
		t.Fatalf(
			"trustedWorkspaces = %#v, want [/other %s]",
			decoded["trustedWorkspaces"],
			paths.Work,
		)
	}

	if _, exists := decoded["customModelsConfig"]; exists {
		t.Fatalf("unexpected customModelsConfig in settings: %#v", decoded)
	}

	if _, err := plan.Headless(""); !errors.Is(err, errAgyEmptyPrompt) {
		t.Fatalf("Headless(\"\") error = %v, want errAgyEmptyPrompt", err)
	}

	headlessArgs, err := plan.Headless("hello")
	if err != nil {
		t.Fatal(err)
	}

	wantHeadless := []string{
		"-p",
		"hello",
		"--model",
		agyDefaultModel,
		"--dangerously-skip-permissions",
	}
	if !slices.Equal(headlessArgs, wantHeadless) {
		t.Fatalf("Headless = %v, want %v", headlessArgs, wantHeadless)
	}

	interactiveEmpty, err := plan.Interactive("")
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(interactiveEmpty, []string{"--model", agyDefaultModel}) {
		t.Fatalf("Interactive(\"\") = %v", interactiveEmpty)
	}

	interactivePrompt, err := plan.Interactive("hello")
	if err != nil {
		t.Fatal(err)
	}

	wantInteractive := []string{"--model", agyDefaultModel, "-i", "hello"}
	if !slices.Equal(interactivePrompt, wantInteractive) {
		t.Fatalf("Interactive(\"hello\") = %v, want %v", interactivePrompt, wantInteractive)
	}

	if plan.ShellTool.Name != "run_command" {
		t.Fatalf("ShellTool.Name = %q, want run_command", plan.ShellTool.Name)
	}

	args := plan.ShellTool.Arguments("printf hi")
	if args["CommandLine"] != "printf hi" || args["WaitMsBeforeAsync"] != agyCommandWaitMs ||
		args["toolSummary"] == "" {
		t.Fatalf("ShellTool.Arguments = %#v", args)
	}
}

func TestAgyTitleRequestRecognition(t *testing.T) {
	checkTitleRequests(t, registry.Harness("agy"), map[string]bool{
		`{"systemInstruction":{"parts":[{"text":"You are a conversation title generator. ` +
			`The message provided is a transcript of a USER's request."}]},"contents":[` +
			`{"role":"user","parts":[{"text":"say hello"}]}]}`: true,
		`{"systemInstruction":{"parts":[{"text":"<identity>\nYou are Antigravity"}]},` +
			`"contents":[{"role":"user","parts":[{"text":"You are a conversation title ` +
			`generator."}]}]}`: false,
		`{"contents":[{"role":"user","parts":[{"text":"You are a conversation title ` +
			`generator."}]}]}`: false,
	})
}

func TestGeminiTitleRequestUserAndToolMatching(t *testing.T) {
	t.Parallel()

	plan := Plan{
		TitleRequests: []TitleRequest{
			{Role: "user", Prefix: "<title-request>", Suffix: "</title-request>"},
			{Tool: "set_session_title"},
		},
	}

	cases := map[string]bool{
		`{"contents":[{"role":"user","parts":[{"text":"<title-request>hello</title-request>"}]}]}`:  true,
		`{"contents":[{"role":"model","parts":[{"text":"<title-request>hello</title-request>"}]}]}`: false,
		`{"tools":[{"functionDeclarations":[{"name":"set_session_title"}]}]}`:                       true,
		`{"tools":[{"functionDeclarations":[{"name":"run_command"}]}]}`:                             false,
	}

	for body, want := range cases {
		var value map[string]any
		if err := json.Unmarshal([]byte(body), &value); err != nil {
			t.Fatal(err)
		}

		if got := plan.TitleRequest(value); got != want {
			t.Errorf("TitleRequest(%s) = %t, want %t", body, got, want)
		}
	}
}
