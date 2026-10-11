package registry

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

type detailRules struct{ policy Policy }

func (detailRules) Known(id Harness) bool   { return id == HarnessClaude }
func (r detailRules) Policy(Harness) Policy { return r.policy }

func detailPolicy(authority Authority) Policy {
	return Policy{Authority: authority, Reporter: "native", RetainNativeActivity: true, Details: DetailCapabilities{Native: DetailSupport{Permission: true, Question: true, UsageLimit: true}, Screen: DetailSupport{Permission: true, Question: true, UsageLimit: true}}}
}

func detailReport(at time.Time, activity *Activity, detail *ActivityDetail) Observation {
	return Observation{Harness: HarnessClaude, At: at, Subject: ObservationIdentity{SessionID: "detail-session"}, Evidence: &Report{Event: "state", Activity: activity, Detail: detail, Claim: new(PresenceLive), Process: &ProcessIdentity{PID: 42, StartIdentity: "boot:42"}, Reporter: Reporter{Integration: "native"}}}
}

func requireDetail(t *testing.T, session Session, value ActivityDetail, quality DetailQuality, at time.Time) {
	t.Helper()
	if session.Detail == nil || session.Detail.Value != value || session.Detail.Quality != quality || !session.Detail.ObservedAt.Equal(at) {
		t.Fatalf("detail = %+v, want %s/%s at %s", session.Detail, value, quality, at)
	}
}

func TestDetailSurvivesMetadataWithoutRenewingEvidence(t *testing.T) {
	t.Parallel()
	store, err := OpenMemoryStore(t.TempDir()+"/state.json", detailRules{policy: detailPolicy(AuthorityHook)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	at := time.Now().UTC().Add(-time.Second)
	clock := at
	store.setNowForTest(func() time.Time { return clock })
	session, err := store.Observe(t.Context(), detailReport(at, new(ActivityWaiting), new(ActivityDetailPermission)))
	if err != nil {
		t.Fatal(err)
	}
	clock = at.Add(10 * time.Second)
	metadata := detailReport(clock, nil, nil)
	metadata.Report().Event = "title.changed"
	if _, err := store.Observe(t.Context(), metadata); err != nil {
		t.Fatal(err)
	}
	current, err := store.Get(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireDetail(t, current, ActivityDetailPermission, DetailQualityCurrent, at)
	state, err := store.State(t.Context(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	clock = at.Add(IntegrationActivityLease + time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	expired, err := store.WaitForRevision(ctx, state.Revision, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	requireDetail(t, expired.Sessions[0], ActivityDetailPermission, DetailQualityStale, at)
	heartbeat := detailReport(clock, new(ActivityWaiting), new(ActivityDetailPermission))
	heartbeat.Report().DetailObservedAt = &at
	if _, err := store.Observe(t.Context(), heartbeat); err != nil {
		t.Fatal(err)
	}

	if err := store.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened := NewFileStore(store.Path(), detailRules{policy: detailPolicy(AuthorityHook)})
	reopened.setNowForTest(func() time.Time { return clock })
	loaded, err := reopened.Get(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireDetail(t, loaded, ActivityDetailPermission, DetailQualityStale, at)
	if !loaded.ActivityChangedAt.Equal(at) {
		t.Fatal("detail aging changed activity age")
	}
}

func TestNativeDetailClearingAndIsolation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		change  func(*Observation)
		want    ActivityDetail
		quality DetailQuality
	}{
		{"generic wait", func(o *Observation) { o.Report().Detail = nil }, "", DetailQualityMissing},
		{"explicit clear", func(o *Observation) { o.Report().Detail = new(ActivityDetail("")) }, "", DetailQualityMissing},
		{"new question", func(o *Observation) { o.Report().Detail = new(ActivityDetailQuestion) }, ActivityDetailQuestion, DetailQualityCurrent},
		{"wrong reporter", func(o *Observation) { o.Report().Reporter.Integration = "foreign" }, "", DetailQualityMissing},
		{"running", func(o *Observation) { o.Report().Activity = new(ActivityRunning); o.Report().Detail = nil }, "", ""},
		{"idle", func(o *Observation) { o.Report().Activity = new(ActivityIdle); o.Report().Detail = nil }, "", ""},
		{"interrupted", func(o *Observation) { o.Report().Activity = new(ActivityInterrupted); o.Report().Detail = nil }, "", ""},
		{"end", func(o *Observation) {
			o.Report().Lifecycle = new(NativeLifecycleEnd)
			o.Report().Claim = new(PresenceGone)
			o.Report().Activity = nil
			o.Report().Detail = nil
		}, "", ""},
		{"new incarnation", func(o *Observation) {
			o.Report().Process.StartIdentity = "boot:43"
			o.Report().Lifecycle = new(NativeLifecycleStart)
		}, "", DetailQualityMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			at := time.Now().UTC().Add(-time.Second)
			store := NewJournal(t.TempDir()+"/state.json", detailRules{policy: detailPolicy(AuthorityHook)})
			if _, err := store.Observe(t.Context(), detailReport(at, new(ActivityWaiting), new(ActivityDetailPermission))); err != nil {
				t.Fatal(err)
			}
			observation := detailReport(at.Add(time.Millisecond), new(ActivityWaiting), new(ActivityDetailPermission))
			test.change(&observation)
			session, err := store.Observe(t.Context(), observation)
			if err != nil {
				t.Fatal(err)
			}
			if test.quality == "" {
				if session.Detail != nil {
					t.Fatalf("inactive detail = %+v", session.Detail)
				}
				return
			}
			observed := time.Time{}
			if test.quality == DetailQualityCurrent {
				observed = observation.At
			}
			requireDetail(t, session, test.want, test.quality, observed)
		})
	}
}

func TestDetailRejectsInvalidBatchAndIgnoresOlderReports(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC().Add(-time.Second)
	store := NewJournal(t.TempDir()+"/state.json", detailRules{policy: detailPolicy(AuthorityHook)})
	session, err := store.Observe(t.Context(), detailReport(at, new(ActivityWaiting), new(ActivityDetailPermission)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observe(t.Context(), detailReport(at.Add(-time.Second), new(ActivityWaiting), new(ActivityDetailQuestion))); !errors.Is(err, ErrObservationConflict) {
		t.Fatal(err)
	}
	_, err = store.ObserveBatch(t.Context(), []Observation{detailReport(at.Add(time.Second), new(ActivityWaiting), new(ActivityDetailQuestion)), detailReport(at.Add(2*time.Second), new(ActivityRunning), new(ActivityDetailUsageLimit))})
	if !errors.Is(err, ErrInvalidObservation) {
		t.Fatalf("invalid batch = %v", err)
	}
	loaded, err := store.Get(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireDetail(t, loaded, ActivityDetailPermission, DetailQualityCurrent, at)
	loaded.Detail.Value = ActivityDetailQuestion
	loaded.Observations.Native.Detail.Value = ActivityDetailQuestion
	loaded, err = store.Get(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireDetail(t, loaded, ActivityDetailPermission, DetailQualityCurrent, at)
	data, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Session
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Detail, decoded.Detail) {
		t.Fatalf("JSON detail = %+v", decoded.Detail)
	}
}

func TestDetailUsesSelectedScreenAuthority(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC().Add(-time.Second)
	store := NewJournal(t.TempDir()+"/state.json", detailRules{policy: detailPolicy(AuthorityScreen)})
	session, err := store.Observe(t.Context(), detailReport(at, new(ActivityWaiting), new(ActivityDetailPermission)))
	if err != nil {
		t.Fatal(err)
	}
	if session.Detail != nil {
		t.Fatal("native detail leaked into unknown screen state")
	}
	reading := ScreenObservation{Activity: ActivityWaiting, Detail: ActivityDetailQuestion, Authority: AuthorityScreen, Reason: "manifest_rule", Process: *session.Process, ObservedAt: at}
	session, err = store.Observe(t.Context(), Observation{Harness: HarnessClaude, At: at, Subject: ObservationIdentity{SessionID: session.SessionID}, Evidence: (*Reading)(&reading)})
	if err != nil {
		t.Fatal(err)
	}
	requireDetail(t, session, ActivityDetailQuestion, DetailQualityCurrent, at)
	if session.Detail.Authority != AuthorityScreen {
		t.Fatal("wrong detail authority")
	}
	reading.Detail = ""
	reading.ObservedAt = at.Add(time.Millisecond)
	session, err = store.Observe(t.Context(), Observation{Harness: HarnessClaude, At: reading.ObservedAt, Subject: ObservationIdentity{SessionID: session.SessionID}, Evidence: (*Reading)(&reading)})
	if err != nil {
		t.Fatal(err)
	}
	requireDetail(t, session, "", DetailQualityMissing, time.Time{})
}

func TestFailureDetailAndUnsupportedWaiting(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		activity Activity
		detail   *ActivityDetail
		value    ActivityDetail
		quality  DetailQuality
	}{
		{ActivityWaiting, nil, "", DetailQualityUnsupported},
		{ActivityFailed, nil, ActivityDetailGeneral, DetailQualityCurrent},
		{ActivityFailed, new(ActivityDetailUsageLimit), ActivityDetailUsageLimit, DetailQualityCurrent},
	} {
		t.Run(string(test.activity)+string(test.value), func(t *testing.T) {
			policy := detailPolicy(AuthorityHook)
			policy.Details.Native = DetailSupport{}
			at := time.Now().UTC().Add(-time.Second)
			store := NewJournal(t.TempDir()+"/state.json", detailRules{policy: policy})
			session, err := store.Observe(t.Context(), detailReport(at, &test.activity, test.detail))
			if err != nil {
				t.Fatal(err)
			}
			observed := time.Time{}
			if test.quality == DetailQualityCurrent {
				observed = at
			}
			requireDetail(t, session, test.value, test.quality, observed)
		})
	}
}

func TestFileWatchPublishesDetailExpiryWithoutNewObservation(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC().Add(-time.Second)
	clock := at
	store := NewJournal(t.TempDir()+"/state.json", detailRules{policy: detailPolicy(AuthorityHook)})
	store.setNowForTest(func() time.Time { return clock })
	if _, err := store.Observe(t.Context(), detailReport(at, new(ActivityWaiting), new(ActivityDetailQuestion))); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	count := 0
	err := store.Watch(ctx, WatchOptions{ReconcileInterval: time.Millisecond}, func(result WatchResult) error {
		if result.Err != nil {
			return result.Err
		}
		count++
		if count == 1 {
			requireDetail(t, result.Sessions[0], ActivityDetailQuestion, DetailQualityCurrent, at)
			clock = at.Add(IntegrationActivityLease + time.Second)
		} else {
			requireDetail(t, result.Sessions[0], ActivityDetailQuestion, DetailQualityStale, at)
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("watch published %d snapshots", count)
	}
}

func TestDetailTimestampRequiresMatchingEvidenceAndCannotFollowReport(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	for _, observation := range []Observation{
		detailReport(at, new(ActivityWaiting), nil),
		detailReport(at, new(ActivityWaiting), new(ActivityDetail(""))),
		detailReport(at, new(ActivityWaiting), new(ActivityDetailPermission)),
	} {
		future := at.Add(time.Second)
		observation.Report().DetailObservedAt = &future
		if err := observation.Validate(detailRules{policy: detailPolicy(AuthorityHook)}); !errors.Is(err, ErrInvalidObservation) {
			t.Fatalf("invalid timestamp = %v", err)
		}
	}
}
