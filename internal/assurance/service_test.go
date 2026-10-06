package assurance

import (
	"context"
	"errors"
	"sync"
	"testing"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

type fixture struct {
	service *Service
	repo    *repository.Repository
}

func newFixture() *fixture {
	repo := repository.New()
	return &fixture{service: NewService(repo), repo: repo}
}

func (f *fixture) openCase(t *testing.T) string {
	t.Helper()
	device, err := f.service.RegisterDevice(context.Background(), contracts.RegisterDeviceRequest{
		AssetTag: "MET-9001",
		Model:    "Bench Meter",
		Room:     "Room A",
		Steward:  "Lin",
	})
	if err != nil {
		t.Fatalf("register device: %v", err)
	}
	view, err := f.service.OpenCase(context.Background(), contracts.OpenCaseRequest{
		DeviceID:  device.ID,
		Nominal:   25.0,
		Tolerance: 0.5,
		Steward:   "Qiao",
	})
	if err != nil {
		t.Fatalf("open case: %v", err)
	}
	return view.Case.ID
}

func (f *fixture) record(t *testing.T, caseID string, observed float64) contracts.CaseResponse {
	t.Helper()
	view, err := f.service.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
		Observed:   observed,
		Technician: "Rui",
	})
	if err != nil {
		t.Fatalf("record measurement %v: %v", observed, err)
	}
	return view
}

func (f *fixture) get(t *testing.T, caseID string) contracts.CaseResponse {
	t.Helper()
	view, err := f.service.Case(context.Background(), caseID)
	if err != nil {
		t.Fatalf("read case: %v", err)
	}
	return view
}

func currentMeasurement(view contracts.CaseResponse) (contracts.MeasurementView, bool) {
	for _, item := range view.Measurements {
		if item.Current {
			return item, true
		}
	}
	return contracts.MeasurementView{}, false
}

// Replacing an out-of-tolerance reading with an in-tolerance one must move the
// case to review_ready, and the opposite correction must pull it back to
// recheck_required. The case conclusion always follows the single current
// reading.
func TestReplaceRecomputesCaseConclusion(t *testing.T) {
	t.Run("rejected then corrected to accepted", func(t *testing.T) {
		f := newFixture()
		caseID := f.openCase(t)
		first := f.record(t, caseID, 26.5)
		if first.Case.State != domain.CaseRecheckRequired {
			t.Fatalf("initial state = %s", first.Case.State)
		}
		oldID := first.Case.ActiveMeasurementID

		replaced, err := f.service.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
			Observed:   25.1,
			Technician: "Rui",
			Replaces:   oldID,
		})
		if err != nil {
			t.Fatalf("replace measurement: %v", err)
		}
		if replaced.Case.State != domain.CaseReviewReady {
			t.Fatalf("state after accepted correction = %s, want review_ready", replaced.Case.State)
		}
		if replaced.Case.ActiveMeasurementID == oldID {
			t.Fatalf("active reading still points at replaced reading %s", oldID)
		}
		current, ok := currentMeasurement(replaced)
		if !ok {
			t.Fatal("no current reading flagged after replacement")
		}
		if !current.Accepted || current.Replaces != oldID {
			t.Fatalf("current reading after replacement: %+v", current.Measurement)
		}
		if len(replaced.Measurements) != 2 {
			t.Fatalf("measurement history = %d entries, want both readings retained", len(replaced.Measurements))
		}
		for _, item := range replaced.Measurements {
			wantCurrent := item.ID == current.ID
			if item.Current != wantCurrent {
				t.Fatalf("reading %s current=%v, want %v", item.ID, item.Current, wantCurrent)
			}
			if item.ID == oldID && item.SupersededBy != current.ID {
				t.Fatalf("old reading superseded_by = %q, want %s", item.SupersededBy, current.ID)
			}
		}
	})

	t.Run("accepted then corrected to rejected", func(t *testing.T) {
		f := newFixture()
		caseID := f.openCase(t)
		first := f.record(t, caseID, 25.2)
		if first.Case.State != domain.CaseReviewReady {
			t.Fatalf("initial state = %s", first.Case.State)
		}
		oldID := first.Case.ActiveMeasurementID

		replaced, err := f.service.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
			Observed:   26.5,
			Technician: "Rui",
			Replaces:   oldID,
		})
		if err != nil {
			t.Fatalf("replace measurement: %v", err)
		}
		if replaced.Case.State != domain.CaseRecheckRequired {
			t.Fatalf("state after rejected correction = %s, want recheck_required", replaced.Case.State)
		}
		if _, err := f.service.SealCase(context.Background(), caseID, contracts.SealCaseRequest{
			Reviewer: "Vera",
			Summary:  "should be impossible",
		}); !errors.Is(err, domain.ErrStateConflict) {
			t.Fatalf("seal after rejected correction err = %v, want state_conflict", err)
		}
		if seal := f.get(t, caseID).Seal; seal != nil {
			t.Fatal("seal was created for an out-of-tolerance current reading")
		}
	})
}

// The seal must cite the current reading, never a superseded one — even when
// the replaced reading had a smaller absolute deviation.
func TestSealReferencesOnlyCurrentReading(t *testing.T) {
	f := newFixture()
	caseID := f.openCase(t)
	first := f.record(t, caseID, 25.1) // deviation 0.1, accepted
	oldID := first.Case.ActiveMeasurementID

	replaced, err := f.service.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
		Observed:   25.4, // deviation 0.4, still accepted but larger
		Technician: "Rui",
		Replaces:   oldID,
	})
	if err != nil {
		t.Fatalf("replace measurement: %v", err)
	}
	current, ok := currentMeasurement(replaced)
	if !ok {
		t.Fatal("no current reading after replacement")
	}

	sealed, err := f.service.SealCase(context.Background(), caseID, contracts.SealCaseRequest{
		Reviewer: "Vera",
		Summary:  "seal on corrected reading",
	})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed.Case.State != domain.CaseSealed {
		t.Fatalf("sealed state = %s", sealed.Case.State)
	}
	if sealed.Seal == nil {
		t.Fatal("seal record missing from response")
	}
	if sealed.Seal.MeasurementID != current.ID {
		t.Fatalf("seal references %s, want current reading %s", sealed.Seal.MeasurementID, current.ID)
	}
	if sealed.Seal.MeasurementID == oldID {
		t.Fatal("seal references the superseded reading")
	}

	// No further mutation is accepted on a sealed case, and the old reading
	// remains on record for traceability.
	persisted := f.get(t, caseID)
	if len(persisted.Measurements) != 2 {
		t.Fatalf("history after seal = %d entries, want 2", len(persisted.Measurements))
	}
}

// A replacement that does not establish must not change anything: no detached
// active id, no pending marker, no new reading, no audit event, same version.
func TestRejectedReplacementLeavesCaseUntouched(t *testing.T) {
	t.Run("invalid technician after valid first phase", func(t *testing.T) {
		f := newFixture()
		caseID := f.openCase(t)
		before := f.record(t, caseID, 26.5)
		oldID := before.Case.ActiveMeasurementID
		versionBefore := before.Case.Version

		_, err := f.service.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
			Observed:   25.1,
			Technician: "   ",
			Replaces:   oldID,
		})
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("replace err = %v, want invalid_input", err)
		}
		after := f.get(t, caseID)
		if after.Case.State != domain.CaseRecheckRequired {
			t.Fatalf("state after failed replacement = %s", after.Case.State)
		}
		if after.Case.ActiveMeasurementID != oldID {
			t.Fatalf("active reading changed to %q after failed replacement", after.Case.ActiveMeasurementID)
		}
		if after.Case.Version != versionBefore {
			t.Fatalf("version changed %d -> %d on failed replacement", versionBefore, after.Case.Version)
		}
		if len(after.Measurements) != 1 {
			t.Fatalf("history changed after failed replacement: %d entries", len(after.Measurements))
		}
		if after.Measurements[0].IsSuperseded() {
			t.Fatalf("reading carries leftover pending marker: %q", after.Measurements[0].SupersededBy)
		}
		if len(after.Events) != len(before.Events) {
			t.Fatalf("audit events changed after failed replacement: %d -> %d", len(before.Events), len(after.Events))
		}
	})

	t.Run("unknown, non-current, and already-replaced targets", func(t *testing.T) {
		f := newFixture()
		caseID := f.openCase(t)
		first := f.record(t, caseID, 25.2)
		oldID := first.Case.ActiveMeasurementID

		if _, err := f.service.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
			Observed: 25.3, Technician: "Rui", Replaces: "measurement-9999",
		}); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("unknown target err = %v, want not_found", err)
		}

		// Establish a superseded reading, then try to replace the stale one.
		second, err := f.service.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
			Observed: 25.3, Technician: "Rui", Replaces: oldID,
		})
		if err != nil {
			t.Fatalf("replace: %v", err)
		}
		newID := second.Case.ActiveMeasurementID
		if _, err := f.service.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
			Observed: 25.4, Technician: "Rui", Replaces: oldID,
		}); !errors.Is(err, domain.ErrStateConflict) {
			t.Fatalf("replace superseded reading err = %v, want state_conflict", err)
		}

		persisted := f.get(t, caseID)
		if persisted.Case.ActiveMeasurementID != newID {
			t.Fatalf("active reading = %s, want %s", persisted.Case.ActiveMeasurementID, newID)
		}
	})

	t.Run("sealed case rejects replacement", func(t *testing.T) {
		f := newFixture()
		caseID := f.openCase(t)
		first := f.record(t, caseID, 25.2)
		oldID := first.Case.ActiveMeasurementID
		if _, err := f.service.SealCase(context.Background(), caseID, contracts.SealCaseRequest{
			Reviewer: "Vera", Summary: "sealed",
		}); err != nil {
			t.Fatalf("seal: %v", err)
		}
		if _, err := f.service.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
			Observed: 25.3, Technician: "Rui", Replaces: oldID,
		}); !errors.Is(err, domain.ErrStateConflict) {
			t.Fatalf("replace on sealed case err = %v, want state_conflict", err)
		}
	})
}

// Replacement and seal racing against each other are serialized by the
// repository lock. Whatever the interleaving, the case must hold exactly one
// coherent result: at most one seal, and if sealed the seal cites the current
// non-superseded reading; otherwise the state matches the current reading.
// The two requests may both be acknowledged only when they chain coherently
// (an accepted correction is sealed), never with contradictory effects.
func TestConcurrentReplaceAndSeal(t *testing.T) {
	cases := []struct {
		name        string
		replacement float64 // corrected reading submitted concurrently with seal
		accepted    bool
	}{
		{name: "accepted correction", replacement: 25.4, accepted: true},
		{name: "rejected correction", replacement: 26.5, accepted: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for iteration := 0; iteration < 100; iteration++ {
				f := newFixture()
				caseID := f.openCase(t)
				first := f.record(t, caseID, 25.1)
				oldID := first.Case.ActiveMeasurementID

				start := make(chan struct{})
				var wg sync.WaitGroup
				results := make([]error, 2)
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-start
					_, results[0] = f.service.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
						Observed: tc.replacement, Technician: "Rui", Replaces: oldID,
					})
				}()
				go func() {
					defer wg.Done()
					<-start
					_, results[1] = f.service.SealCase(context.Background(), caseID, contracts.SealCaseRequest{
						Reviewer: "Vera", Summary: "concurrent seal",
					})
				}()
				close(start)
				wg.Wait()

				replaceErr := results[0]
				sealErr := results[1]
				final := f.get(t, caseID)
				current, hasCurrent := currentMeasurement(final)

				if sealErr == nil && replaceErr == nil {
					// Coherent chain only: the replacement had to be accepted
					// (otherwise sealing it would have failed), and the seal
					// must cite the replacement rather than the old reading.
					if !tc.accepted {
						t.Fatalf("iteration %d: rejected replacement and seal both succeeded", iteration)
					}
					if final.Case.State != domain.CaseSealed || final.Seal == nil {
						t.Fatalf("iteration %d: both succeeded but state=%s seal=%v", iteration, final.Case.State, final.Seal)
					}
					if final.Seal.MeasurementID != final.Case.ActiveMeasurementID || final.Seal.MeasurementID != current.ID {
						t.Fatalf("iteration %d: seal %s does not cite current reading %s", iteration, final.Seal.MeasurementID, final.Case.ActiveMeasurementID)
					}
					if current.ID == oldID || current.Replaces != oldID {
						t.Fatalf("iteration %d: chained seal did not absorb the replacement", iteration)
					}
				} else if sealErr == nil {
					// Seal won: it references the reading that was current at
					// seal time, and the losing replacement changed nothing.
					if replaceErr == nil {
						t.Fatalf("iteration %d: impossible outcome branch", iteration)
					}
					if final.Case.State != domain.CaseSealed || final.Seal == nil {
						t.Fatalf("iteration %d: seal reported success but state=%s seal=%v", iteration, final.Case.State, final.Seal)
					}
					if final.Seal.MeasurementID != final.Case.ActiveMeasurementID {
						t.Fatalf("iteration %d: seal references %s but active reading is %s", iteration, final.Seal.MeasurementID, final.Case.ActiveMeasurementID)
					}
					if !hasCurrent || !current.Accepted || current.IsSuperseded() {
						t.Fatalf("iteration %d: sealed current reading is not a valid accepted reading", iteration)
					}
					if len(final.Measurements) != 1 {
						t.Fatalf("iteration %d: losing replacement appended a reading", iteration)
					}
				} else if replaceErr == nil {
					// Replacement won: no seal exists and the conclusion
					// matches the corrected reading.
					if final.Seal != nil {
						t.Fatalf("iteration %d: replacement won but a seal exists", iteration)
					}
					wantState := domain.CaseReviewReady
					if !tc.accepted {
						wantState = domain.CaseRecheckRequired
					}
					if final.Case.State != wantState {
						t.Fatalf("iteration %d: state=%s, want %s", iteration, final.Case.State, wantState)
					}
					if !hasCurrent || current.Accepted != tc.accepted || current.Replaces != oldID {
						t.Fatalf("iteration %d: current reading does not match the replacement", iteration)
					}
				} else {
					t.Fatalf("iteration %d: both operations failed: replace=%v seal=%v", iteration, replaceErr, sealErr)
				}

				// No matter who won, the history stays complete and exactly
				// one reading is flagged current, with no pending markers.
				currentCount := 0
				for _, item := range final.Measurements {
					if item.Current {
						currentCount++
					}
					if item.SupersededBy == "pending-replacement" {
						t.Fatalf("iteration %d: reading %s carries a pending replacement marker", iteration, item.ID)
					}
				}
				if currentCount != 1 {
					t.Fatalf("iteration %d: %d readings flagged current, want 1", iteration, currentCount)
				}
			}
		})
	}
}

// Two identical replacements racing each other cannot both succeed and cannot
// leave the pending marker behind.
func TestConcurrentDuplicateReplacements(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		f := newFixture()
		caseID := f.openCase(t)
		first := f.record(t, caseID, 26.5)
		oldID := first.Case.ActiveMeasurementID

		start := make(chan struct{})
		var wg sync.WaitGroup
		results := make([]error, 2)
		attempt := func(slot int) {
			defer wg.Done()
			<-start
			_, results[slot] = f.service.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
				Observed: 25.1, Technician: "Rui", Replaces: oldID,
			})
		}
		wg.Add(2)
		go attempt(0)
		go attempt(1)
		close(start)
		wg.Wait()

		successes, conflicts := 0, 0
		for _, err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, domain.ErrStateConflict):
				conflicts++
			default:
				t.Fatalf("iteration %d: unexpected replace err %v", iteration, err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("iteration %d: %d success, %d conflict", iteration, successes, conflicts)
		}
		final := f.get(t, caseID)
		if final.Case.State != domain.CaseReviewReady {
			t.Fatalf("iteration %d: state = %s", iteration, final.Case.State)
		}
		current, ok := currentMeasurement(final)
		if !ok || !current.Accepted || current.Replaces != oldID {
			t.Fatalf("iteration %d: bad current reading after duplicate replacement", iteration)
		}
		if len(final.Measurements) != 2 {
			t.Fatalf("iteration %d: history = %d entries, want 2", iteration, len(final.Measurements))
		}
	}
}
