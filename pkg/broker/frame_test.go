package broker

import (
	"errors"
	"strings"
	"testing"
)

func TestResponseFrameLimit(t *testing.T) {
	_, err := readResponse(responseScanner(strings.NewReader(strings.Repeat(" ", MaxResponseBytes))))
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("oversized frame error = %v, want ErrProtocol", err)
	}
}

func TestResponseFramesAreIndividuallyDecoded(t *testing.T) {
	scanner := responseScanner(strings.NewReader("{\"version\":2}\n{\"version\":2}\n"))
	for range 2 {
		response, err := readResponse(scanner)
		if err != nil || response.Version != ProtocolVersion {
			t.Fatalf("response = %#v, error = %v", response, err)
		}
	}
}
