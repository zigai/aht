package prime

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zigai/aht/v2/internal/command"
	"github.com/zigai/aht/v2/internal/harness"
	"github.com/zigai/aht/v2/internal/processinfo"
	"github.com/zigai/aht/v2/pkg/registry"
)

func TestDefinition(t *testing.T) {
	definition := New().Definition()
	if definition.ID != registry.Harness("prime-agent") ||
		!reflect.DeepEqual(definition.Aliases, []string{"prime", "primeagent", "prime_agent"}) ||
		!reflect.DeepEqual(definition.ProcessNames, []string{"prime-agent"}) {
		t.Fatalf("identity = %#v", definition)
	}
	if definition.ExclusiveProcess || definition.CatalogCreates ||
		definition.Capabilities.SessionStart || definition.Capabilities.SessionEnd ||
		!definition.Capabilities.RunningIdle || definition.Capabilities.WaitingPermission ||
		!definition.Capabilities.ProcessIdentity || !definition.Capabilities.NativeCatalog ||
		definition.Capabilities.TTYTmuxContext {
		t.Fatalf("capabilities = %#v", definition)
	}
	if len(definition.Env.SessionID) != 0 || len(definition.Env.SessionPath) != 0 || len(definition.Env.PID) != 0 || len(definition.Env.Event) != 0 ||
		!reflect.DeepEqual(definition.Env.ProjectRoot, []string{"PRIME_AGENT_INTERNAL_DAEMON_WORKER_CWD"}) {
		t.Fatalf("environment = %#v", definition.Env)
	}
	if definition.IntegrationSource != "prime-agent-catalog" ||
		definition.IntegrationVersion != 1 ||
		definition.StateAuthority != registry.AuthorityHook || definition.ScreenFallback {
		t.Fatalf("integration = %#v", definition)
	}
}

func TestInstallPlanDeclaresNativeCatalog(t *testing.T) {
	plan, err := New().InstallPlan("unused")
	if err != nil || len(plan.Actions) != 1 {
		t.Fatalf("InstallPlan() = %#v, %v", plan, err)
	}
	action, ok := plan.Actions[0].(harness.NativeCatalogAction)
	if !ok || action.Label != "native session catalog" ||
		!reflect.DeepEqual(action.Command, []string{"prime-agent", "sessions", "--all", "--json"}) {
		t.Fatalf("action = %#v", plan.Actions[0])
	}
	var _ harness.NoInstallRequired = New()
}

func TestDistributionInstallUsesPinnedOfficialInstaller(t *testing.T) {
	distribution := New().Distribution()
	if distribution.Source != "channel" || distribution.Directory != "prime" || distribution.Install == nil {
		t.Fatalf("Distribution() = %#v", distribution)
	}
	commands := distribution.Install("1.2.3", "/work", "/home/user/.local/bin")
	want := []harness.DistributionCommand{
		{Name: "curl", Args: []string{"--fail", "--silent", "--show-error", "--location", "--retry", "3", "--max-time", "180", "--output", "/work/prime-agent-install.sh", primeInstallerURL}, Env: nil},
		{Name: "sh", Args: []string{"/work/prime-agent-install.sh"}, Env: []string{"PRIME_AGENT_VERSION=1.2.3", "PRIME_AGENT_RUST_PREFIX=/home/user/.local"}},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("install commands = %#v, want %#v", commands, want)
	}
}

func TestResumeCommandPrefersPath(t *testing.T) {
	adapter := New()
	for _, test := range []struct {
		id, path string
		want     []string
	}{
		{id: "durable", path: "/sessions/durable.jsonl", want: []string{"prime-agent", "--resume", "/sessions/durable.jsonl"}},
		{id: "durable", want: []string{"prime-agent", "--resume", "durable"}},
		{},
	} {
		if got := adapter.ResumeCommand(test.id, test.path); !reflect.DeepEqual(got, test.want) {
			t.Errorf("ResumeCommand(%q, %q) = %v, want %v", test.id, test.path, got, test.want)
		}
	}
}

func TestObservableProcessRequiresPrimeWorkerExecutable(t *testing.T) {
	adapter := New()
	for _, test := range []struct {
		process processinfo.Process
		want    bool
	}{
		{process: processinfo.Process{Executable: "/usr/bin/prime-agent", Args: []string{"prime-agent", "worker"}}, want: true},
		{process: processinfo.Process{Executable: "/opt/bin/prime-agent", Args: []string{"/opt/bin/prime-agent", "worker", "--descriptor", "path"}}, want: true},
		{process: processinfo.Process{Executable: "/usr/bin/other", AgentHint: "prime-agent", Args: []string{"prime-agent", "worker"}}},
		{process: processinfo.Process{Args: []string{"prime-agent", "worker"}}},
		{process: processinfo.Process{Executable: "/usr/bin/prime-agent", Args: []string{"prime-agent", "--mode", "daemon"}}},
		{process: processinfo.Process{Executable: "/usr/bin/prime-agent", Args: []string{"prime-agent", "sessions", "--all", "--json"}}},
		{process: processinfo.Process{Executable: "/usr/bin/prime-agent", Args: []string{"prime-agent", "status"}}},
		{process: processinfo.Process{Executable: "/usr/bin/prime-agent", Args: []string{"prime-agent", "update"}}},
		{process: processinfo.Process{Executable: "/usr/bin/prime-agent", Args: []string{"prime-agent", "prompt", "worker"}}},
		{process: processinfo.Process{Executable: "/usr/bin/prime-agent", Args: []string{"prime-agent"}}},
		{},
	} {
		if got := adapter.ObservableProcess(test.process); got != test.want {
			t.Errorf("ObservableProcess(%#v) = %t, want %t", test.process, got, test.want)
		}
	}
}

func TestParseCatalog(t *testing.T) {
	data := []byte(`{"sessions":[
		{"id":"active-route","activeSessionId":"active-route","sessionId":"durable","sessionFile":"/sessions/durable.jsonl","sessionName":"Native name","firstMessage":"First prompt","cwd":"/work/project","activity":"working","lifecycle":"live","runtimeKind":"subagent","workerPid":42,"rlmDepth":1,"parentSessionId":"parent","rlmChildId":"child"},
		{"id":"saved","activeSessionId":"saved","sessionId":"saved","sessionFile":"/sessions/saved.jsonl","firstMessage":"Saved prompt","cwd":"/work/other","activity":"idle","lifecycle":"archived"}
	]}`)
	sessions, err := parseCatalog(data)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("parseCatalog() = %#v, %v", sessions, err)
	}
	active := sessions[0]
	if active.SessionID != "durable" || active.SessionPath != "/sessions/durable.jsonl" ||
		active.CWD != "/work/project" || active.ProjectRoot != active.CWD || active.ProcessPID != 42 ||
		!active.Current || active.Activity != registry.ActivityRunning || active.Title != "Native name" ||
		active.Attributes["prime_session_name"] != "Native name" || active.Attributes["prime_first_message"] != "First prompt" ||
		active.Attributes["prime_parent_session_id"] != "parent" || active.Attributes["prime_rlm_child_id"] != "child" ||
		active.Attributes["prime_rlm_depth"] != "1" || active.Attributes["prime_runtime_kind"] != "subagent" ||
		!reflect.DeepEqual(active.ResumeCommand, []string{"prime-agent", "--resume", "/sessions/durable.jsonl"}) {
		t.Fatalf("active session = %#v", active)
	}
	saved := sessions[1]
	if saved.Current || saved.ProcessPID != 0 || saved.Activity != "" || saved.Title != "" ||
		saved.Attributes["prime_first_message"] != "Saved prompt" {
		t.Fatalf("saved session = %#v", saved)
	}
}

func TestQuotaParkedActivityDetail(t *testing.T) {
	sessions, err := parseCatalog([]byte(`{"sessions":[{"sessionId":"quota","workerPid":42,"activity":"idle","isQuotaParked":true}]}`))
	if err != nil || len(sessions) != 1 {
		t.Fatalf("parseCatalog() = %#v, %v", sessions, err)
	}
	session := sessions[0]
	if session.Activity != registry.ActivityFailed || session.Attributes["prime_quota_parked"] != "true" {
		t.Fatalf("quota session = %#v", session)
	}
	detail := New().ActivityDetail("", session.Activity, session.Attributes)
	if detail == nil || *detail != registry.ActivityDetailUsageLimit {
		t.Fatalf("ActivityDetail() = %v", detail)
	}
	capabilities := New().DetailCapabilities()
	if !capabilities.Native.UsageLimit || capabilities.Native.Permission || capabilities.Native.Question {
		t.Fatalf("DetailCapabilities() = %#v", capabilities)
	}
}

func TestParseCatalogRejectsMalformedOrMissingIdentity(t *testing.T) {
	for _, data := range [][]byte{
		[]byte(`{"sessions":`),
		[]byte(`{}`),
		[]byte(`{"sessions":[{"sessionId":""}]}`),
	} {
		if _, err := parseCatalog(data); err == nil {
			t.Errorf("parseCatalog(%s) succeeded", data)
		}
	}
}

func TestParseCatalogReturnsPartialRowsAndIgnoresFutureActivity(t *testing.T) {
	sessions, err := parseCatalog([]byte(`{"sessions":[
		{"sessionId":"valid","activity":"future-state","workerPid":7},
		{"sessionId":""}
	]}`))
	if err == nil || len(sessions) != 1 || sessions[0].SessionID != "valid" || sessions[0].Activity != "" {
		t.Fatalf("parseCatalog() = %#v, %v", sessions, err)
	}
}

func TestCatalogUnavailableWithoutBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := New().Catalog(t.Context())
	if !errors.Is(err, harness.ErrCatalogUnavailable) {
		t.Fatalf("Catalog() error = %v, want unavailable", err)
	}
}

func TestCatalogUnavailableWithoutDaemon(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, primeAgentCommand)
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binary, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("PRIME_AGENT_DAEMON_SOCKET", filepath.Join(dir, "daemon.sock"))
	_, err := New().Catalog(t.Context())
	if !errors.Is(err, harness.ErrCatalogUnavailable) {
		t.Fatalf("Catalog() error = %v, want unavailable", err)
	}
}

func listenDaemonSocket(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	path := "daemon.sock"
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	t.Setenv("PRIME_AGENT_DAEMON_SOCKET", path)
}

func TestCatalogCommandFailuresAreNotUnavailable(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("requires a POSIX shell")
	}
	for _, test := range []struct {
		name   string
		script string
		want   error
	}{
		{name: "nonzero", script: "#!/bin/sh\nprintf 'daemon failed\\n' >&2\nexit 7\n"},
		{name: "output limit", script: "#!/bin/sh\n/bin/cat /dev/zero\n", want: command.ErrOutputLimit},
		{name: "timeout", script: "#!/bin/sh\n/bin/sleep 6\n", want: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, primeAgentCommand)
			if err := os.WriteFile(binary, []byte(test.script), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(binary, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			listenDaemonSocket(t)
			_, err := New().Catalog(t.Context())
			if err == nil || errors.Is(err, harness.ErrCatalogUnavailable) {
				t.Fatalf("Catalog() error = %v, want command failure without unavailable", err)
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("Catalog() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCatalogPreservesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := New().Catalog(ctx)
	if !errors.Is(err, context.Canceled) || errors.Is(err, harness.ErrCatalogUnavailable) {
		t.Fatalf("Catalog() error = %v, want caller cancellation", err)
	}
}

func TestLocationsAndTranscriptSourcesHonorOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", "~/custom-agent")
	t.Setenv("PRIME_AGENT_SESSION_DIR", "~/custom-sessions")
	locations := New().Locations("/project")
	wantConfig := filepath.Join(home, "custom-agent", "settings.json")
	found := false
	for _, location := range locations {
		if location.Kind == harness.LocationKindConfig && location.Path == wantConfig {
			found = true
		}
	}
	if !found {
		t.Fatalf("locations lack %q: %#v", wantConfig, locations)
	}
	wantSources := []string{filepath.Join(home, "custom-sessions"), filepath.Join(home, "custom-agent", "session-artifacts")}
	if got, err := transcriptSources(home); err != nil || !reflect.DeepEqual(got, wantSources) {
		t.Fatalf("transcriptSources() = %v, %v", got, err)
	}
}

func TestTranscriptSourcesUseRequestedHome(t *testing.T) {
	t.Setenv("HOME", filepath.Join(t.TempDir(), "ambient"))
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", "")
	t.Setenv("PRIME_AGENT_SESSION_DIR", "")
	requested := filepath.Join(t.TempDir(), "requested")
	want := []string{
		filepath.Join(requested, ".prime", "agent", "sessions"),
		filepath.Join(requested, ".prime", "agent", "session-artifacts"),
	}
	if got, err := transcriptSources(requested); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("transcriptSources() = %v, %v, want %v", got, err, want)
	}
}

func TestSessionTitlesPreferCatalogAndScanTranscript(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	body := `{"type":"session","id":"durable","cwd":"/work"}
{"type":"message","message":{"role":"user","content":"First user prompt"}}
{"type":"session_info","name":"First title"}
{"type":"session_info","name":"Last title"}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{
		{SessionID: "durable", SessionPath: path, Attributes: map[string]string{"prime_session_name": "Catalog title", "prime_first_message": "Catalog prompt"}},
		{SessionID: "durable", SessionPath: path},
		{SessionID: "saved", Attributes: map[string]string{"prime_first_message": "Saved prompt"}},
	})
	if err != nil || !reflect.DeepEqual(titles, []string{"Catalog title", "Last title", "Saved prompt"}) {
		t.Fatalf("SessionTitles() = %v, %v", titles, err)
	}
}

func TestSessionTitleRejectsMismatchedTranscriptIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"other","title":"Wrong title"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{{SessionID: "durable", SessionPath: path}})
	if err != nil || !reflect.DeepEqual(titles, []string{""}) {
		t.Fatalf("SessionTitles() = %v, %v", titles, err)
	}
}

func TestSessionTitleFallsBackToFirstUserMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"session","id":"durable"}
{"type":"message","message":{"role":"assistant","content":"ignore"}}
{"type":"message","message":{"role":"user","content":[{"type":"text","text":"First"},{"type":"text","text":"prompt"}]}}
{"type":"message","message":{"role":"user","content":"Later prompt"}}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	titles, err := New().SessionTitles(t.Context(), []registry.ObservationIdentity{{SessionID: "durable", SessionPath: path}})
	if err != nil || !reflect.DeepEqual(titles, []string{"First prompt"}) {
		t.Fatalf("SessionTitles() = %v, %v", titles, err)
	}
}
