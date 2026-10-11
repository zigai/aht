package agentstate

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/zigai/aht/v2/pkg/registry"
)

func FuzzNormalizeSnapshotBounded(f *testing.F) {
	f.Add("\x1b[31mworking\x1b[0m\nline", "\x1b]0;codex\a")
	f.Add("", "")
	f.Add("line\n\n", "title")
	f.Add("\x1b[\x1b[31m31m", "\x1b[\x1b[31m31m")
	f.Fuzz(func(t *testing.T, screen string, title string) {
		snapshot := NormalizeSnapshot(screen, title)
		if len(snapshot.Lines) > maxSnapshotLines {
			t.Fatalf("snapshot has %d lines, maximum is %d", len(snapshot.Lines), maxSnapshotLines)
		}
		encoded := strings.Join(snapshot.Lines, "\n")
		if len(snapshot.Lines) > 0 {
			encoded += "\n"
		}
		next := NormalizeSnapshot(encoded, snapshot.Title)
		if !slices.Equal(snapshot.Lines, next.Lines) || snapshot.Title != next.Title {
			t.Fatalf("normalization is not idempotent: first=%#v second=%#v", snapshot, next)
		}
		requireSnapshotWithoutEscapes(t, snapshot)
		requireSnapshotContentPreserved(t, screen, title, snapshot)
		requireTerminalSequencesStripped(t, screen, title)
	})
}

func requireSnapshotContentPreserved(t *testing.T, screen, title string, snapshot Snapshot) {
	t.Helper()
	if !strings.ContainsAny(screen, "\x1b\r\n") && screen != "" && !slices.Equal(snapshot.Lines, []string{screen}) {
		t.Fatalf("plain screen content changed: %q -> %q", screen, snapshot.Lines)
	}
	if !strings.ContainsRune(title, '\x1b') && snapshot.Title != title {
		t.Fatalf("plain title changed: %q -> %q", title, snapshot.Title)
	}
}

func requireTerminalSequencesStripped(t *testing.T, screen, title string) {
	t.Helper()
	plainTitle := strings.ReplaceAll(title, "\x1b", "")
	plainLine := strings.Map(func(character rune) rune {
		if character == '\x1b' || character == '\r' || character == '\n' {
			return -1
		}
		return character
	}, screen)
	var expectedLines []string
	if plainLine != "" {
		expectedLines = []string{plainLine}
	}
	for _, decoration := range [][2]string{
		{"\x1b[31m", "\x1b[0m"},
		{"\x1b]0;first\x1b\\", "\x1b]0;second\x1b\\"},
		{"\x1b]2;first\a", "\x1b]2;second\a"},
	} {
		decorated := NormalizeSnapshot(decoration[0]+plainLine+decoration[1], decoration[0]+plainTitle+decoration[1])
		if !slices.Equal(decorated.Lines, expectedLines) || decorated.Title != plainTitle {
			t.Fatalf("terminal sequences %q changed content: %#v, want lines=%q title=%q", decoration, decorated, expectedLines, plainTitle)
		}
	}
}

func requireSnapshotWithoutEscapes(t *testing.T, snapshot Snapshot) {
	t.Helper()
	if strings.ContainsRune(snapshot.Title, '\x1b') || slices.ContainsFunc(snapshot.Lines, func(line string) bool { return strings.ContainsRune(line, '\x1b') }) {
		t.Fatalf("normalized snapshot kept a terminal escape: %#v", snapshot)
	}
}

func FuzzParseManifest(f *testing.F) {
	f.Add([]byte("version=1\nagent='codex'\n[[rules]]\nid='idle'\nstate='idle'\nany=['ready']\n"))
	f.Add([]byte("not toml"))
	f.Add([]byte("version=2\nagent='codex'\n"))
	f.Add([]byte("version=1\nagent='codex'\nunknown=true\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		manifest, err := ParseManifest(data, registry.Harness("codex"))
		if len(data) > maxManifestBytes {
			requireManifestRejection(t, manifest, err, errManifestTooLarge)
			return
		}
		var document map[string]any
		if syntaxErr := toml.Unmarshal(data, &document); syntaxErr != nil {
			requireManifestRejection(t, manifest, err, errManifestInvalid)
			return
		}
		if err != nil {
			requireManifestRejection(t, manifest, err, errManifestInvalid)
			return
		}
		if manifest.Version != 1 || manifest.Agent != "codex" || len(manifest.Rules) == 0 {
			t.Fatalf("accepted an invalid manifest: %#v", manifest)
		}
		unknown := append([]byte("unknown_manifest_field = true\n"), data...)
		invalid, invalidErr := ParseManifest(unknown, registry.Harness("codex"))
		category := errManifestInvalid
		if len(unknown) > maxManifestBytes {
			category = errManifestTooLarge
		}
		requireManifestRejection(t, invalid, invalidErr, category)
	})
}

func FuzzManifestDefinitions(f *testing.F) {
	for violation := range uint8(14) {
		f.Add("sample", "ready", 3, violation)
		f.Add("\"\n\u754c", "\\[\x7f", -7, violation)
	}
	f.Fuzz(func(t *testing.T, id, literal string, priority int, violation uint8) {
		id = "rule-" + strings.ToValidUTF8(id, "\ufffd")
		literal = "match-" + strings.ToValidUTF8(literal, "\ufffd")
		rules := []map[string]any{
			{"id": id, "state": "idle", "priority": priority, "region": "bottom:3", "case_sensitive": priority%2 == 0, "any": []string{literal}},
			{"id": "other-" + id, "state": "running", "priority": 0, "all": []string{"other-" + literal}, "none": []string{id}},
		}
		document := map[string]any{"version": 1, "agent": "codex", "rules": rules}
		applyManifestViolation(document, rules, violation%14)
		data, err := toml.Marshal(document)
		if err != nil {
			t.Fatalf("encode generated definition: %v", err)
		}
		if violation%14 == 12 {
			data = append(data, []byte("\ninvalid = [")...)
		}
		if violation%14 == 13 {
			data = append(data, []byte(strings.Repeat(" ", maxManifestBytes+1))...)
		}
		manifest, parseErr := ParseManifest(data, registry.Harness("codex"))
		if len(data) > maxManifestBytes {
			requireManifestRejection(t, manifest, parseErr, errManifestTooLarge)
			return
		}
		if violation%14 != 0 {
			requireManifestRejection(t, manifest, parseErr, errManifestInvalid)
			return
		}
		if parseErr != nil {
			t.Fatalf("valid generated manifest rejected: %v\n%s", parseErr, data)
		}
		requireGeneratedManifest(t, manifest, id, literal, priority)
	})
}

func applyManifestViolation(document map[string]any, rules []map[string]any, violation uint8) {
	switch violation {
	case 1:
		document["version"] = 2
	case 2:
		document["agent"] = "other"
	case 3:
		document["rules"] = []map[string]any{}
	case 11:
		document["unknown_manifest_field"] = true
	default:
		applyManifestRuleViolation(rules, violation)
	}
}

func applyManifestRuleViolation(rules []map[string]any, violation uint8) {
	switch violation {
	case 4:
		rules[0]["id"] = " \t"
	case 5:
		rules[1]["id"] = rules[0]["id"]
	case 6:
		rules[0]["state"] = "unknown"
	case 7:
		rules[0]["detail"] = "permission"
	case 8:
		rules[0]["region"] = "side:3"
	case 9:
		delete(rules[0], "any")
	case 10:
		rules[0]["regex_any"] = []string{"["}
	}
}

func requireGeneratedManifest(t *testing.T, manifest Manifest, id, literal string, priority int) {
	t.Helper()
	if manifest.Version != 1 || manifest.Agent != "codex" || len(manifest.Rules) != 2 {
		t.Fatalf("generated manifest header changed: %#v", manifest)
	}
	if manifest.Rules[0].ID != id || manifest.Rules[0].Priority != priority || !slices.Equal(manifest.Rules[0].Any, []string{literal}) || manifest.Rules[1].ID != "other-"+id {
		t.Fatalf("generated rule changed: %#v", manifest.Rules)
	}
}

func requireManifestRejection(t *testing.T, manifest Manifest, err, category error) {
	t.Helper()
	if !errors.Is(err, category) {
		t.Fatalf("manifest error = %v, want %v", err, category)
	}
	if !reflect.DeepEqual(manifest, Manifest{}) {
		t.Fatalf("rejected manifest returned partial data: %#v", manifest)
	}
}
