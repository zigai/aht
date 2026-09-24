package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseDurationDayBounds(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"106752d", "-106752d", "213504d", "9223372036854775807d", "-9223372036854775808d"} {
		if _, err := ParseDuration(input); !errors.Is(err, ErrInvalidDuration) {
			t.Errorf("ParseDuration(%q) error = %v, want ErrInvalidDuration", input, err)
		}
	}
	const largestWholeDays = 106751 * 24 * time.Hour
	for _, tc := range []struct {
		input string
		want  time.Duration
	}{
		{input: "106751d", want: largestWholeDays},
		{input: "-106751d", want: -largestWholeDays},
	} {
		got, err := ParseDuration(tc.input)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("ParseDuration(%q) = %s, want %s", tc.input, got, tc.want)
		}
	}
}

func TestLoadNormalizesUIValues(t *testing.T) {
	isolateConfigEnv(t)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[ui]\nsort = ' Time '\ndefault_presence = ' ALL '\ntime_format = ' ISO8601 '\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Sort != "updated" || cfg.UI.DefaultPresence != "all" || cfg.UI.TimeFormat != "iso8601" {
		t.Fatalf("noncanonical UI configuration: %+v", cfg.UI)
	}
	if err := os.WriteFile(path, []byte("[ui]\nsort = ' Agent '\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = Load(path)
	if err != nil || cfg.UI.Sort != "harness" {
		t.Fatalf("file alias: sort = %q, error = %v", cfg.UI.Sort, err)
	}
}

func TestLoadPreservesFilesystemError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(path, "config.toml")
	_, statErr := os.Stat(invalid)
	if statErr == nil {
		t.Fatal("expected a non-directory path component error")
	}
	_, _, err := Load(invalid)
	if !errors.Is(err, ErrAccessConfig) || errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("Load error = %v, want access error, not missing", err)
	}
	pathErr, ok := errors.AsType[*os.PathError](statErr)
	if !ok || !errors.Is(err, pathErr.Err) {
		t.Fatalf("Load error does not retain OS error: %v", err)
	}
}

func TestConcurrentEnsurePublishesOneCompleteConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	var createdCount atomic.Int32
	var group sync.WaitGroup
	const contenders = 16
	for range contenders {
		group.Go(func() {
			created, err := EnsureConfigFile(path)
			if err != nil {
				t.Error(err)
				return
			}
			if created {
				createdCount.Add(1)
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != DefaultConfigTemplate() {
				t.Errorf("partially published config: %q, error: %v", contents, err)
			}
		})
	}
	group.Wait()
	if got := createdCount.Load(); got != 1 {
		t.Errorf("successful creators = %d, want 1", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config permissions = %o", info.Mode().Perm())
	}
}

func TestWriteConfigReplacesWithoutMutatingExistingInode(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("old config"), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()
	if err := WriteConfigFile(path); err != nil {
		t.Fatal(err)
	}
	contents := make([]byte, len("old config"))
	if _, err := old.Read(contents); err != nil || string(contents) != "old config" {
		t.Fatalf("existing inode was overwritten: %q, %v", contents, err)
	}
}

func TestReadBoundedFileBoundsActualRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(strings.Repeat("#", maxConfigFileSize+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedFile(path); !errors.Is(err, ErrConfigFileTooLarge) {
		t.Fatalf("readBoundedFile error = %v, want size limit", err)
	}
}
