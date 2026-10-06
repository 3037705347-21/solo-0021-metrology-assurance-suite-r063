package domain

import (
	"fmt"
	"strings"
	"time"
)

type Measurement struct {
	ID           string    `json:"id"`
	CaseID       string    `json:"case_id"`
	Observed     float64   `json:"observed"`
	Nominal      float64   `json:"nominal"`
	Deviation    float64   `json:"deviation"`
	Tolerance    float64   `json:"tolerance"`
	Accepted     bool      `json:"accepted"`
	Technician   string    `json:"technician"`
	CreatedAt    time.Time `json:"created_at"`
	Replaces     string    `json:"replaces,omitempty"`
	SupersededBy string    `json:"superseded_by,omitempty"`
}

func NewMeasurement(id, caseID string, observed, nominal, tolerance float64, technician string, at time.Time) (Measurement, error) {
	item := Measurement{
		ID:         strings.TrimSpace(id),
		CaseID:     strings.TrimSpace(caseID),
		Observed:   observed,
		Nominal:    nominal,
		Deviation:  observed - nominal,
		Tolerance:  tolerance,
		Accepted:   abs(observed-nominal) <= tolerance,
		Technician: strings.TrimSpace(technician),
		CreatedAt:  at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return Measurement{}, err
	}
	return item, nil
}

func (m Measurement) Validate() error {
	if m.ID == "" || m.CaseID == "" || m.Technician == "" {
		return fmt.Errorf("%w: measurement id, case id, and technician are required", ErrInvalidInput)
	}
	if !isFinite(m.Observed) || !isFinite(m.Nominal) || !isFinite(m.Tolerance) || m.Tolerance <= 0 {
		return fmt.Errorf("%w: observed and nominal must be finite and tolerance must be positive", ErrInvalidInput)
	}
	if m.CreatedAt.IsZero() {
		return fmt.Errorf("%w: measurement time is required", ErrInvalidInput)
	}
	return nil
}

func (m Measurement) AbsoluteDeviation() float64 {
	return abs(m.Deviation)
}

func (m Measurement) IsSuperseded() bool {
	return strings.TrimSpace(m.SupersededBy) != ""
}

func (m Measurement) Identifies(measurementID string) bool {
	return m.ID == strings.TrimSpace(measurementID)
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
