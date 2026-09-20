package kimi

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPayloadDefaultsPreservesSessionLookupFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_SHARE_DIR", home)
	// A regular file where the sessions directory belongs produces a portable
	// filesystem error, including when the tests run with elevated permissions.
	if err := os.WriteFile(filepath.Join(home, "sessions"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New().PayloadDefaults(map[string]any{"session_id": "wanted"})
	if _, ok := errors.AsType[*os.PathError](err); !ok {
		t.Fatalf("error = %v, want session lookup path error", err)
	}
}

func TestKimiCodeSessionPathReturnsCurrentSessionDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_SHARE_DIR", home)
	sessionPath := filepath.Join(home, "sessions", "work-hash", "wanted")
	if err := os.MkdirAll(sessionPath, 0o700); err != nil {
		t.Fatal(err)
	}

	path, err := kimiCodeSessionPath("wanted")
	if err != nil {
		t.Fatal(err)
	}
	if path != sessionPath {
		t.Fatalf("session path = %q, want %q", path, sessionPath)
	}
}

func TestKimiCodeSessionPathRejectsPathTraversal(t *testing.T) {
	t.Setenv("KIMI_SHARE_DIR", t.TempDir())

	path, err := kimiCodeSessionPath("../wanted")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("session path = %q, want empty", path)
	}
}

func TestTOMLQuoteStringRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []string{
		`simple`,
		`with "quotes" and 'single quotes'`,
		`with \backslashes\ and /slashes/`,
		"with unicode: 🚀 and 世界 and ñ", //nolint:gosmopolitan // test fixture validates UTF-8 multi-byte rune handling in TOML strings
		"with bell: \a",
		"with vertical tab: \v",
		"with delete: \x7f",
		"with mixed: \"\a\v\x7f\\🚀",
	}
	for _, tc := range cases {
		quoted := tomlQuoteString(tc)
		if strings.Contains(quoted, "\x7f") {
			t.Errorf("quoted string contains literal DEL: %q", quoted)
		}
		if strings.Contains(quoted, `\a`) || strings.Contains(quoted, `\v`) {
			t.Errorf("quoted string contains invalid TOML escape \\a or \\v: %q", quoted)
		}
		var unquoted string
		if err := json.Unmarshal([]byte(quoted), &unquoted); err != nil {
			t.Errorf("unmarshal failed for %q: %v", quoted, err)
		}
		if unquoted != tc {
			t.Errorf("roundtrip mismatch: got %q, want %q", unquoted, tc)
		}
	}
}
