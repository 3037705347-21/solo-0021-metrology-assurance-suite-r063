package repository

import (
	"context"
	"fmt"
	"sync"

	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
)

type Repository struct {
	mu           sync.RWMutex
	devices      map[string]domain.Device
	cases        map[string]domain.VerificationCase
	measurements map[string][]domain.Measurement
	seals        map[string]domain.EvidenceSeal
	events       []domain.AuditEvent
	counters     map[string]int
}

func New() *Repository {
	return &Repository{
		devices:      make(map[string]domain.Device),
		cases:        make(map[string]domain.VerificationCase),
		measurements: make(map[string][]domain.Measurement),
		seals:        make(map[string]domain.EvidenceSeal),
		counters:     make(map[string]int),
	}
}

type Snapshot struct {
	devices      map[string]domain.Device
	cases        map[string]domain.VerificationCase
	measurements map[string][]domain.Measurement
	seals        map[string]domain.EvidenceSeal
	events       []domain.AuditEvent
}

func (s Snapshot) Device(id string) (domain.Device, bool) {
	item, ok := s.devices[id]
	return item, ok
}

func (s Snapshot) DeviceByTag(tag string) (domain.Device, bool) {
	for _, item := range s.devices {
		if item.AssetTag == tag {
			return item, true
		}
	}
	return domain.Device{}, false
}

func (s Snapshot) Case(id string) (domain.VerificationCase, bool) {
	item, ok := s.cases[id]
	return item, ok
}

func (s Snapshot) Measurements(caseID string) []domain.Measurement {
	return append([]domain.Measurement(nil), s.measurements[caseID]...)
}

func (s Snapshot) Seal(caseID string) (domain.EvidenceSeal, bool) {
	item, ok := s.seals[caseID]
	return item, ok
}

func (s Snapshot) Events(caseID string) []domain.AuditEvent {
	var result []domain.AuditEvent
	for _, item := range s.events {
		if item.CaseID == caseID {
			result = append(result, item)
		}
	}
	return result
}

func (s Snapshot) AllDevices() []domain.Device {
	result := make([]domain.Device, 0, len(s.devices))
	for _, item := range s.devices {
		result = append(result, item)
	}
	return result
}

func (s Snapshot) AllCases() []domain.VerificationCase {
	result := make([]domain.VerificationCase, 0, len(s.cases))
	for _, item := range s.cases {
		result = append(result, item)
	}
	return result
}

type Mutation struct {
	devices      map[string]domain.Device
	cases        map[string]domain.VerificationCase
	measurements map[string][]domain.Measurement
	seals        map[string]domain.EvidenceSeal
	events       []domain.AuditEvent
	counters     map[string]int
}

func (m *Mutation) NextID(prefix string) string {
	m.counters[prefix]++
	return fmt.Sprintf("%s-%04d", prefix, m.counters[prefix])
}

func (m *Mutation) Device(id string) (domain.Device, bool) {
	item, ok := m.devices[id]
	return item, ok
}

func (m *Mutation) DeviceByTag(tag string) (domain.Device, bool) {
	for _, item := range m.devices {
		if item.AssetTag == tag {
			return item, true
		}
	}
	return domain.Device{}, false
}

func (m *Mutation) PutDevice(item domain.Device) error {
	if _, exists := m.devices[item.ID]; exists {
		return fmt.Errorf("%w: device %s", domain.ErrStateConflict, item.ID)
	}
	m.devices[item.ID] = item
	return nil
}

func (m *Mutation) UpdateDevice(item domain.Device) error {
	if _, exists := m.devices[item.ID]; !exists {
		return fmt.Errorf("%w: device %s", domain.ErrNotFound, item.ID)
	}
	m.devices[item.ID] = item
	return nil
}

func (m *Mutation) Case(id string) (domain.VerificationCase, bool) {
	item, ok := m.cases[id]
	return item, ok
}

func (m *Mutation) PutCase(item domain.VerificationCase) error {
	if _, exists := m.cases[item.ID]; exists {
		return fmt.Errorf("%w: case %s", domain.ErrStateConflict, item.ID)
	}
	m.cases[item.ID] = item
	return nil
}

func (m *Mutation) UpdateCase(item domain.VerificationCase) error {
	if _, exists := m.cases[item.ID]; !exists {
		return fmt.Errorf("%w: case %s", domain.ErrNotFound, item.ID)
	}
	m.cases[item.ID] = item
	return nil
}

func (m *Mutation) ActiveCaseForDevice(deviceID string) (domain.VerificationCase, bool) {
	for _, item := range m.cases {
		if item.DeviceID == deviceID && item.IsUnfinished() {
			return item, true
		}
	}
	return domain.VerificationCase{}, false
}

func (m *Mutation) AppendMeasurement(item domain.Measurement) error {
	if _, exists := m.cases[item.CaseID]; !exists {
		return fmt.Errorf("%w: case %s", domain.ErrNotFound, item.CaseID)
	}
	m.measurements[item.CaseID] = append(m.measurements[item.CaseID], item)
	return nil
}

func (m *Mutation) Measurements(caseID string) []domain.Measurement {
	return append([]domain.Measurement(nil), m.measurements[caseID]...)
}

func (m *Mutation) Measurement(caseID, measurementID string) (domain.Measurement, bool) {
	for _, item := range m.measurements[caseID] {
		if item.ID == measurementID {
			return item, true
		}
	}
	return domain.Measurement{}, false
}

func (m *Mutation) ReplaceMeasurement(item domain.Measurement) error {
	items, exists := m.measurements[item.CaseID]
	if !exists {
		return fmt.Errorf("%w: case %s", domain.ErrNotFound, item.CaseID)
	}
	for index := range items {
		if items[index].ID == item.ID {
			items[index] = item
			m.measurements[item.CaseID] = items
			return nil
		}
	}
	return fmt.Errorf("%w: measurement %s", domain.ErrNotFound, item.ID)
}

func (m *Mutation) Seal(caseID string) (domain.EvidenceSeal, bool) {
	item, ok := m.seals[caseID]
	return item, ok
}

func (m *Mutation) PutSeal(item domain.EvidenceSeal) error {
	if _, exists := m.seals[item.CaseID]; exists {
		return fmt.Errorf("%w: case %s", domain.ErrSealAlreadyCreated, item.CaseID)
	}
	m.seals[item.CaseID] = item
	return nil
}

func (m *Mutation) AppendEvent(item domain.AuditEvent) {
	m.events = append(m.events, item)
}

func (m *Mutation) Events(caseID string) []domain.AuditEvent {
	var result []domain.AuditEvent
	for _, item := range m.events {
		if item.CaseID == caseID {
			result = append(result, item)
		}
	}
	return result
}

func (r *Repository) Read(ctx context.Context, fn func(Snapshot) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return fn(r.snapshot())
}

func (r *Repository) Mutate(ctx context.Context, fn func(*Mutation) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	next := &Mutation{
		devices:      cloneDevices(r.devices),
		cases:        cloneCases(r.cases),
		measurements: cloneMeasurements(r.measurements),
		seals:        cloneSeals(r.seals),
		events:       append([]domain.AuditEvent(nil), r.events...),
		counters:     cloneCounters(r.counters),
	}
	if err := fn(next); err != nil {
		return err
	}
	r.devices = next.devices
	r.cases = next.cases
	r.measurements = next.measurements
	r.seals = next.seals
	r.events = next.events
	r.counters = next.counters
	return nil
}

func (r *Repository) snapshot() Snapshot {
	return Snapshot{
		devices:      cloneDevices(r.devices),
		cases:        cloneCases(r.cases),
		measurements: cloneMeasurements(r.measurements),
		seals:        cloneSeals(r.seals),
		events:       append([]domain.AuditEvent(nil), r.events...),
	}
}

func cloneDevices(values map[string]domain.Device) map[string]domain.Device {
	result := make(map[string]domain.Device, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneCases(values map[string]domain.VerificationCase) map[string]domain.VerificationCase {
	result := make(map[string]domain.VerificationCase, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneMeasurements(values map[string][]domain.Measurement) map[string][]domain.Measurement {
	result := make(map[string][]domain.Measurement, len(values))
	for key, value := range values {
		result[key] = append([]domain.Measurement(nil), value...)
	}
	return result
}

func cloneSeals(values map[string]domain.EvidenceSeal) map[string]domain.EvidenceSeal {
	result := make(map[string]domain.EvidenceSeal, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneCounters(values map[string]int) map[string]int {
	result := make(map[string]int, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
