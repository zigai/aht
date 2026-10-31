package launch

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zigai/aht/v2/internal/harness"
)

func TestCloseStopsServicesAndKeepsRoot(t *testing.T) {
	t.Parallel()
	address := freeAddress(t)
	plan := harness.LaunchPlan{Services: []harness.LaunchService{{
		Args:    []string{helperArgument, "listen", address},
		Address: address,
	}}}

	s, _, err := newFixtureHarness(t.Context(), t, plan, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if !accepting(t.Context(), address) {
		t.Fatal("service does not accept connections after New returned")
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if accepting(t.Context(), address) {
		t.Fatal("service still accepts connections after Close")
	}

	if _, err := os.Stat(s.Root); err != nil {
		t.Fatalf("Close removed the root: %v", err)
	}
}

func TestNewStopsStartedServicesWhenLaterServiceExits(t *testing.T) {
	t.Parallel()
	address := freeAddress(t)
	plan := harness.LaunchPlan{Services: []harness.LaunchService{
		{Args: []string{helperArgument, "listen", address}, Address: address},
		{Args: []string{helperArgument, "exit"}, Address: freeAddress(t)},
	}}

	_, _, err := newFixtureHarness(t.Context(), t, plan, Options{})
	if !errors.Is(err, errExitedEarly) {
		t.Fatalf("New() error = %v, want errExitedEarly", err)
	}

	if accepting(t.Context(), address) {
		t.Fatal("first service outlived the failed New")
	}
}

func TestNewReportsServiceThatNeverListens(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	plan := harness.LaunchPlan{Services: []harness.LaunchService{{
		Args:    []string{helperArgument, "sleep"},
		Address: freeAddress(t),
	}}}

	_, _, err := newFixtureHarness(ctx, t, plan, Options{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("New() error = %v, want the context deadline", err)
	}
}

func TestNewRejectsMalformedServiceAddress(t *testing.T) {
	t.Parallel()

	plan := harness.LaunchPlan{Services: []harness.LaunchService{{
		Args:    []string{helperArgument, "sleep"},
		Address: "no-port",
	}}}
	if _, _, err := newFixtureHarness(t.Context(), t, plan, Options{}); err == nil {
		t.Fatal("New() accepted an address without a port")
	}
}

func TestStartTrackerWaitsForBrokerSocketUntilClose(t *testing.T) {
	t.Parallel()
	options := Options{Root: shortRoot(t), AHT: fakeAHT(t)}

	s, _, err := newFixtureHarness(t.Context(), t, harness.LaunchPlan{}, options)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.StartTracker(t.Context()); err != nil {
		t.Fatal(err)
	}

	socket := s.trackerSocket()

	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("%s is not a socket", socket)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	var dialer net.Dialer
	if conn, err := dialer.DialContext(t.Context(), "unix", socket); err == nil {
		_ = conn.Close()

		t.Fatal("tracker socket still accepts connections after Close")
	}
}

func TestStartTrackerReportsTrackerThatExits(t *testing.T) {
	t.Parallel()
	options := Options{
		Root: shortRoot(t),
		AHT:  fakeAHT(t),
		Env:  []string{trackerModeEnv + "=exit"},
	}

	s, _, err := newFixtureHarness(t.Context(), t, harness.LaunchPlan{}, options)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.StartTracker(t.Context()); !errors.Is(err, errExitedEarly) {
		t.Fatalf("StartTracker() error = %v, want errExitedEarly", err)
	}

	if _, err := os.Stat(filepath.Join(s.Root, "logs", "tracker.log")); err != nil {
		t.Fatalf("tracker log missing: %v", err)
	}
}

func TestStartTrackerRequiresAHT(t *testing.T) {
	t.Parallel()

	s, _, err := newFixtureHarness(t.Context(), t, harness.LaunchPlan{}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.StartTracker(t.Context()); !errors.Is(err, errNoAHT) {
		t.Fatalf("StartTracker() error = %v, want errNoAHT", err)
	}
}
