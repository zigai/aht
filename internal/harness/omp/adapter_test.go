package omp

import (
	"os"
	"path/filepath"
	"testing"
)

func isolateDirEnvironment(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"PI_CODING_AGENT_DIR", "PI_CONFIG_DIR", "OMP_PROFILE", "PI_PROFILE", "XDG_DATA_HOME"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestAgentDir(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want func(home string) string
	}{
		{
			name: "default",
			want: func(home string) string { return filepath.Join(home, ".omp", "agent") },
		},
		{
			name: "config dir name replaces the default",
			env:  map[string]string{"PI_CONFIG_DIR": ".custom-omp"},
			want: func(home string) string { return filepath.Join(home, ".custom-omp", "agent") },
		},
		{
			name: "absolute config dir is joined under home",
			env:  map[string]string{"PI_CONFIG_DIR": "/srv/omp"},
			want: func(home string) string { return filepath.Join(home, "srv", "omp", "agent") },
		},
		{
			name: "agent dir override replaces the default profile agent dir",
			env:  map[string]string{"PI_CODING_AGENT_DIR": "/srv/agent", "PI_CONFIG_DIR": ".custom-omp"},
			want: func(string) string { return "/srv/agent" },
		},
		{
			name: "named profile",
			env:  map[string]string{"OMP_PROFILE": "work"},
			want: func(home string) string { return filepath.Join(home, ".omp", "profiles", "work", "agent") },
		},
		{
			name: "named profile follows the config dir name",
			env:  map[string]string{"OMP_PROFILE": "work", "PI_CONFIG_DIR": ".custom-omp"},
			want: func(home string) string { return filepath.Join(home, ".custom-omp", "profiles", "work", "agent") },
		},
		{
			name: "named profile ignores the agent dir override",
			env:  map[string]string{"OMP_PROFILE": "work", "PI_CODING_AGENT_DIR": "/srv/agent"},
			want: func(home string) string { return filepath.Join(home, ".omp", "profiles", "work", "agent") },
		},
		{
			name: "legacy profile variable is used when the canonical one is unset",
			env:  map[string]string{"PI_PROFILE": "legacy"},
			want: func(home string) string { return filepath.Join(home, ".omp", "profiles", "legacy", "agent") },
		},
		{
			name: "canonical profile wins over the legacy one",
			env:  map[string]string{"OMP_PROFILE": "work", "PI_PROFILE": "legacy"},
			want: func(home string) string { return filepath.Join(home, ".omp", "profiles", "work", "agent") },
		},
		{
			name: "explicitly empty canonical profile selects the default despite the legacy one",
			env:  map[string]string{"OMP_PROFILE": "", "PI_PROFILE": "legacy", "PI_CODING_AGENT_DIR": "/srv/agent"},
			want: func(string) string { return "/srv/agent" },
		},
		{
			name: "profile named default selects the default profile",
			env:  map[string]string{"OMP_PROFILE": "default"},
			want: func(home string) string { return filepath.Join(home, ".omp", "agent") },
		},
		{
			name: "unrelated variable is ignored",
			env:  map[string]string{"OMP_CONFIG_DIR": "/srv/ignored"},
			want: func(home string) string { return filepath.Join(home, ".omp", "agent") },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := isolateDirEnvironment(t)
			for name, value := range test.env {
				t.Setenv(name, value)
			}
			if got, want := ompAgentDir(), test.want(home); got != want {
				t.Fatalf("ompAgentDir() = %q, want %q", got, want)
			}
		})
	}
}

func TestSessionsDir(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		dataDirs []string
		want     func(home, data string) string
	}{
		{
			name: "default profile",
			want: func(home, _ string) string { return filepath.Join(home, ".omp", "agent", "sessions") },
		},
		{
			name: "config dir name",
			env:  map[string]string{"PI_CONFIG_DIR": ".custom-omp"},
			want: func(home, _ string) string { return filepath.Join(home, ".custom-omp", "agent", "sessions") },
		},
		{
			name: "named profile",
			env:  map[string]string{"OMP_PROFILE": "work"},
			want: func(home, _ string) string {
				return filepath.Join(home, ".omp", "profiles", "work", "agent", "sessions")
			},
		},
		{
			name: "agent dir override",
			env:  map[string]string{"PI_CODING_AGENT_DIR": "/srv/agent"},
			want: func(string, string) string { return "/srv/agent/sessions" },
		},
		{
			name:     "existing data home root is used",
			dataDirs: []string{"omp"},
			want:     func(_, data string) string { return filepath.Join(data, "omp", "sessions") },
		},
		{
			name:     "data home without an omp root is ignored",
			dataDirs: []string{},
			want:     func(home, _ string) string { return filepath.Join(home, ".omp", "agent", "sessions") },
		},
		{
			name:     "named profile ignores a data home root that lacks its profile",
			env:      map[string]string{"OMP_PROFILE": "work"},
			dataDirs: []string{"omp"},
			want: func(home, _ string) string {
				return filepath.Join(home, ".omp", "profiles", "work", "agent", "sessions")
			},
		},
		{
			name:     "named profile uses its own data home root",
			env:      map[string]string{"OMP_PROFILE": "work"},
			dataDirs: []string{filepath.Join("omp", "profiles", "work")},
			want:     func(_, data string) string { return filepath.Join(data, "omp", "profiles", "work", "sessions") },
		},
		{
			name:     "agent dir override disables the data home root",
			env:      map[string]string{"PI_CODING_AGENT_DIR": "/srv/agent"},
			dataDirs: []string{"omp"},
			want:     func(string, string) string { return "/srv/agent/sessions" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := isolateDirEnvironment(t)
			data := t.TempDir()
			if test.dataDirs != nil {
				t.Setenv("XDG_DATA_HOME", data)
			}
			for _, dir := range test.dataDirs {
				if err := os.MkdirAll(filepath.Join(data, dir), 0o750); err != nil {
					t.Fatal(err)
				}
			}
			for name, value := range test.env {
				t.Setenv(name, value)
			}
			if got, want := sessionsDirUnder(home), test.want(home, data); got != want {
				t.Fatalf("sessionsDirUnder() = %q, want %q", got, want)
			}
		})
	}
}
