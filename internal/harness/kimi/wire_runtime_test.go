package kimi

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

var errPublicationFailed = errors.New("publication failed")

func TestPumpPreservesNativeBytes(t *testing.T) {
	t.Parallel()
	file, err := os.CreateTemp(t.TempDir(), "wire")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	input := " {\"extension\":\"" + strings.Repeat("x", 9000) + "\"} \r\n{\"last\":true}"
	if err := pumpFrames(strings.NewReader(input), file, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte(input)) {
		t.Fatal("native frame bytes changed")
	}
}

func TestPumpDoesNotForwardUnpublishedApproval(t *testing.T) {
	t.Parallel()
	file, err := os.CreateTemp(t.TempDir(), "wire")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	blocked := errPublicationFailed
	err = pumpFrames(strings.NewReader("{\"method\":\"request\"}\n"), file, func([]byte) error { return blocked })
	if !errors.Is(err, blocked) {
		t.Fatalf("publication error = %v", err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatal("approval escaped before state publication")
	}
}

func TestPumpRejectsOversizedFrameWithoutPayloadLeak(t *testing.T) {
	t.Parallel()
	file, err := os.CreateTemp(t.TempDir(), "wire")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	secret := "private-host-output"
	err = pumpFrames(strings.NewReader(secret+strings.Repeat("x", frameLimit)), file, func([]byte) error { return nil })
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe frame error: %v", err)
	}
	info, statErr := file.Stat()
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Size() != 0 {
		t.Fatal("oversized frame was partially forwarded")
	}
}
