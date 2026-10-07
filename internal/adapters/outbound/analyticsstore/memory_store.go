package analyticsstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/claudioed/product-master/internal/analytics/report"
)

// Memory is the in-memory twin of Projection and Reader, for unit tests and
// the HTTP handler tests. It implements the same semantics as the SQL (the
// shared contract test runs against both): idempotent on the event id,
// earliest-candidate milestones, the profile version guard, day windows from
// report.DayWindows, and a rejection of what the database's CHECK
// constraints would reject.
type Memory struct {
	mu        sync.Mutex
	processed map[string]struct{}
	facts     map[string]*memFact
	states    map[string]map[int64]memState
	lastAt    *time.Time
}

type memFact struct {
	firsts         report.Firsts
	profileVersion int64
	current        report.ProfileState
}

type memState struct {
	at time.Time
	report.ProfileState
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{processed: map[string]struct{}{}, facts: map[string]*memFact{}, states: map[string]map[int64]memState{}}
}

var (
	_ report.Projection = (*Memory)(nil)
	_ report.Reader     = (*Memory)(nil)
)

// Apply implements report.Projection.
func (m *Memory) Apply(_ context.Context, e report.ProductEvent) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.processed[e.EventID]; dup {
		return false, nil
	}
	if err := checkLikeTheDatabase(e); err != nil {
		return false, err
	}
	m.processed[e.EventID] = struct{}{}
	at := e.At.UTC()
	if m.lastAt == nil || at.After(*m.lastAt) {
		m.lastAt = &at
	}
	f, ok := m.facts[e.SKU]
	if !ok {
		f = &memFact{}
		m.facts[e.SKU] = f
	}
	c := report.FirstsOf(e)
	f.firsts.Registered = earliest(f.firsts.Registered, c.Registered)
	f.firsts.Classified = earliest(f.firsts.Classified, c.Classified)
	f.firsts.DimensionsDeclared = earliest(f.firsts.DimensionsDeclared, c.DimensionsDeclared)
	f.firsts.Measured = earliest(f.firsts.Measured, c.Measured)
	if s := e.Profile; s != nil {
		if m.states[e.SKU] == nil {
			m.states[e.SKU] = map[int64]memState{}
		}
		if _, exists := m.states[e.SKU][e.Version]; !exists {
			m.states[e.SKU][e.Version] = memState{at: at, ProfileState: *s}
		}
		if e.Version > f.profileVersion {
			f.profileVersion, f.current = e.Version, *s
		}
	}
	return true, nil
}

// checkLikeTheDatabase mirrors the analytical schema's CHECK constraints
// (a profile version >= 1; a discrepancy needs both declared and measured):
// what Postgres rejects with an integrity violation, Memory rejects too.
func checkLikeTheDatabase(e report.ProductEvent) error {
	s := e.Profile
	if s == nil {
		return nil
	}
	if e.Version < 1 || (s.Discrepancy && (!s.HasDeclared || !s.HasMeasured)) {
		return fmt.Errorf("%w: profile state %s v%d violates a check constraint", report.ErrRejected, e.SKU, e.Version)
	}
	return nil
}

// earliest is LEAST with SQL NULL semantics: nil candidates are ignored.
func earliest(stored, candidate *time.Time) *time.Time {
	if candidate == nil {
		return stored
	}
	if stored == nil || candidate.Before(*stored) {
		t := *candidate
		return &t
	}
	return stored
}

// inWindow is the half-open [From, To) test.
func inWindow(t *time.Time, w report.DayWindow) bool {
	return t != nil && t.Compare(w.From) >= 0 && t.Compare(w.To) < 0
}

// QualityDays implements report.Reader.
func (m *Memory) QualityDays(_ context.Context, r report.Range) ([]report.QualityDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	windows := report.DayWindows(r)
	out := make([]report.QualityDay, 0, len(windows))
	for _, w := range windows {
		d := report.QualityDay{Day: w.Day}
		for _, f := range m.facts {
			d.Registered += count(inWindow(f.firsts.Registered, w))
			d.Classified += count(inWindow(f.firsts.Classified, w))
			d.DimensionsDeclared += count(inWindow(f.firsts.DimensionsDeclared, w))
			d.Measured += count(inWindow(f.firsts.Measured, w))
		}
		for _, states := range m.states {
			d.OpenDiscrepancies += count(latestBefore(states, w.To).Discrepancy)
		}
		out = append(out, d)
	}
	return out, nil
}

// latestBefore is the state with the highest version among those that
// occurred strictly before end (the zero state when there is none).
func latestBefore(states map[int64]memState, end time.Time) memState {
	versions := make([]int64, 0, len(states))
	for v := range states {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
	for _, v := range versions {
		if states[v].at.Before(end) {
			return states[v]
		}
	}
	return memState{}
}

func count(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Coverage implements report.Reader.
func (m *Memory) Coverage(_ context.Context) (report.CoverageCounts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := report.CoverageCounts{Products: len(m.facts)}
	for _, f := range m.facts {
		c.Classified += count(f.firsts.Classified != nil)
		c.DimensionsDeclared += count(f.current.HasDeclared)
		c.Measured += count(f.current.HasMeasured)
		c.OpenDiscrepancies += count(f.current.Discrepancy)
	}
	return c, nil
}

// LastEventAt implements report.Reader.
func (m *Memory) LastEventAt(_ context.Context) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastAt == nil {
		return nil, nil
	}
	at := *m.lastAt
	return &at, nil
}
