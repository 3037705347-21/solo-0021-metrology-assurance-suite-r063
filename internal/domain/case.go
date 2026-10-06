package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

type CaseState string

const (
	CaseAwaitingMeasurement CaseState = "awaiting_measurement"
	CaseReviewReady         CaseState = "review_ready"
	CaseRecheckRequired     CaseState = "recheck_required"
	CaseSealed              CaseState = "sealed"
)

type VerificationCase struct {
	ID                  string     `json:"id"`
	DeviceID            string     `json:"device_id"`
	Nominal             float64    `json:"nominal"`
	Tolerance           float64    `json:"tolerance"`
	Steward             string     `json:"steward"`
	State               CaseState  `json:"state"`
	Version             int        `json:"version"`
	OpenedAt            time.Time  `json:"opened_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	SealedAt            *time.Time `json:"sealed_at,omitempty"`
	ActiveMeasurementID string     `json:"active_measurement_id,omitempty"`
}

func NewVerificationCase(id, deviceID string, nominal, tolerance float64, steward string, at time.Time) (VerificationCase, error) {
	item := VerificationCase{
		ID:        strings.TrimSpace(id),
		DeviceID:  strings.TrimSpace(deviceID),
		Nominal:   nominal,
		Tolerance: tolerance,
		Steward:   strings.TrimSpace(steward),
		State:     CaseAwaitingMeasurement,
		Version:   1,
		OpenedAt:  at.UTC(),
		UpdatedAt: at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return VerificationCase{}, err
	}
	return item, nil
}

func (c VerificationCase) Validate() error {
	if c.ID == "" || c.DeviceID == "" || c.Steward == "" {
		return fmt.Errorf("%w: case id, device id, and steward are required", ErrInvalidInput)
	}
	if !isFinite(c.Nominal) || !isFinite(c.Tolerance) || c.Tolerance <= 0 {
		return fmt.Errorf("%w: nominal must be finite and tolerance must be positive", ErrInvalidInput)
	}
	if c.Version < 1 || c.OpenedAt.IsZero() || c.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: case version and timestamps are required", ErrInvalidInput)
	}
	switch c.State {
	case CaseAwaitingMeasurement, CaseReviewReady, CaseRecheckRequired, CaseSealed:
		return nil
	default:
		return fmt.Errorf("%w: unsupported case state %q", ErrInvalidInput, c.State)
	}
}

func (c VerificationCase) IsUnfinished() bool {
	return c.State != CaseSealed
}

func (c VerificationCase) ActiveMeasurementIDMatches(measurementID string) bool {
	current := strings.TrimSpace(c.ActiveMeasurementID)
	return current != "" && current == strings.TrimSpace(measurementID)
}

func (c *VerificationCase) RecordMeasurement(accepted bool, measurementID string, at time.Time) error {
	if c.State != CaseAwaitingMeasurement {
		return fmt.Errorf("%w: case is not awaiting measurement", ErrStateConflict)
	}
	c.adoptReading(measurementID, accepted, at)
	return nil
}

func (c *VerificationCase) ReplaceMeasurement(measurementID string, accepted bool, at time.Time) error {
	if c.State == CaseSealed {
		return fmt.Errorf("%w: a sealed case cannot accept a replacement", ErrStateConflict)
	}
	if strings.TrimSpace(c.ActiveMeasurementID) == "" {
		return fmt.Errorf("%w: the case has no current reading to replace", ErrStateConflict)
	}
	c.adoptReading(measurementID, accepted, at)
	return nil
}

func (c *VerificationCase) adoptReading(measurementID string, accepted bool, at time.Time) {
	c.ActiveMeasurementID = strings.TrimSpace(measurementID)
	if accepted {
		c.State = CaseReviewReady
	} else {
		c.State = CaseRecheckRequired
	}
	c.Version++
	c.UpdatedAt = at.UTC()
}

func (c *VerificationCase) Reopen(at time.Time) error {
	if c.State != CaseRecheckRequired {
		return fmt.Errorf("%w: only a recheck_required case can be reopened", ErrStateConflict)
	}
	c.State = CaseAwaitingMeasurement
	c.Version++
	c.UpdatedAt = at.UTC()
	return nil
}

func (c *VerificationCase) Seal(at time.Time) error {
	if c.State != CaseReviewReady {
		return fmt.Errorf("%w: case is not ready for review", ErrStateConflict)
	}
	stamp := at.UTC()
	c.State = CaseSealed
	c.Version++
	c.UpdatedAt = stamp
	c.SealedAt = &stamp
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
