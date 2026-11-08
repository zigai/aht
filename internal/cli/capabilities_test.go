package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/aht/v2/pkg/harness"
)

func TestManageCapabilitiesTable(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := executeCLI(t.Context(), []string{"manage", "capabilities"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %s", code, stderr.String())
	}

	capabilities := harness.AllCapabilities()
	if len(capabilities) == 0 {
		t.Fatal("catalog has no capabilities")
	}

	for _, capability := range capabilities {
		if !strings.Contains(stdout.String(), string(capability.Harness)) {
			t.Errorf("table omits %s: %s", capability.Harness, stdout.String())
		}
	}
}

func TestManageCapabilitiesJSON(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := executeCLI(
		t.Context(),
		[]string{"--json", "manage", "capabilities"},
		nil,
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("exit code = %d; stderr = %s", code, stderr.String())
	}

	var got []harness.Capabilities
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	want := harness.AllCapabilities()
	if len(want) == 0 {
		t.Fatal("catalog has no capabilities")
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("CLI capabilities differ from the library (-want +got):\n%s", diff)
	}
}

func TestManageCapabilitiesSingleHarness(t *testing.T) {
	t.Parallel()

	capabilities := harness.AllCapabilities()
	if len(capabilities) == 0 {
		t.Fatal("catalog has no capabilities")
	}

	for _, capability := range capabilities {
		t.Run(string(capability.Harness), func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			args := []string{
				"--json",
				"manage",
				"capabilities",
				"--harness",
				string(capability.Harness),
			}
			if code := executeCLI(t.Context(), args, nil, &stdout, &stderr); code != 0 {
				t.Fatalf("exit code = %d; stderr = %s", code, stderr.String())
			}

			var got harness.Capabilities
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(capability, got); diff != "" {
				t.Fatalf("filtered capabilities (-want +got):\n%s", diff)
			}

			stdout.Reset()

			if code := executeCLI(t.Context(), args[1:], nil, &stdout, &stderr); code != 0 {
				t.Fatalf("human capabilities exit code = %d; stderr = %s", code, stderr.String())
			}

			for _, value := range []string{"Harness:", string(capability.Harness), "Authority:", capability.Authority} {
				if !strings.Contains(stdout.String(), value) {
					t.Errorf("human capabilities omit %q: %s", value, stdout.String())
				}
			}
		})
	}

	t.Run("invalid harness exits usage code 2", func(t *testing.T) {
		t.Parallel()

		var stdout, stderr bytes.Buffer

		code := executeCLI(
			t.Context(),
			[]string{"manage", "capabilities", "--harness", "invalid-harness"},
			nil,
			&stdout,
			&stderr,
		)
		if code != exitCodeUsage || stdout.Len() != 0 ||
			!strings.Contains(stderr.String(), "unknown harness") {
			t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
		}
	})
}
