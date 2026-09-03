// Copyright (c) 2025 JoeGlenn1213
// ActionD Job Store (In-Memory)

package store

import (
	"fmt"
	"sync"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/job"
)

// Store interface for job persistence
type Store interface {
	AddJob(job *job.ActionJob) error
	UpdateJob(job *job.ActionJob) error
	FinishJob(job *job.ActionJob) error
	GetJob(id string) (*job.ActionJob, error)
	ListJobs(limit int) ([]*job.ActionJob, error)
	ListJobsByEventID(eventID string) ([]*job.ActionJob, error)
	ListRecoverableJobs(since time.Time) ([]*job.ActionJob, error)
	AbandonStaleJobs(cutoff time.Time) (int, error)
	DeleteJob(id string) error
	DeleteJobsBefore(cutoff time.Time) (int, error)
}

// MemoryStore implements Store in memory
type MemoryStore struct {
	mu   sync.RWMutex
	jobs []*job.ActionJob
	m    map[string]*job.ActionJob
}

// NewMemoryStore creates a new in-memory store
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		jobs: make([]*job.ActionJob, 0),
		m:    make(map[string]*job.ActionJob),
	}
}

func (s *MemoryStore) AddJob(j *job.ActionJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Store a deep copy so the worker goroutine that keeps mutating the
	// original never races with store readers (go test -race verified).
	cp := j.Clone()
	s.jobs = append([]*job.ActionJob{cp}, s.jobs...) // Prepend for LIFO-ish listing (newest first)
	s.m[j.ID] = cp
	return nil
}

func (s *MemoryStore) UpdateJob(j *job.ActionJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.m[j.ID]
	if !ok {
		return nil
	}
	existing.Status = j.Status
	existing.Progress = j.Progress
	existing.StartedAt = cloneTimePtr(j.StartedAt)
	if j.EventJSON != "" {
		existing.EventJSON = j.EventJSON
	}
	return nil
}

func (s *MemoryStore) FinishJob(j *job.ActionJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.m[j.ID]
	if !ok {
		return nil
	}
	// Update fields
	existing.Status = j.Status
	existing.Progress = j.Progress
	if existing.Progress == "" {
		existing.Progress = "Done"
		switch j.Status {
		case job.StatusFailed:
			existing.Progress = "Failed"
		case job.StatusCanceled:
			existing.Progress = "Cancelled"
		}
	}
	existing.Error = j.Error
	existing.ErrorSummary = j.ErrorSummary
	existing.ExitCode = j.ExitCode
	existing.CompletedAt = j.CompletedAt
	existing.DurationMs = j.DurationMs
	existing.Duration = j.Duration
	existing.EndedAt = cloneTimePtr(j.EndedAt)
	existing.Artifacts = append([]string(nil), j.Artifacts...)
	if j.Result != nil {
		existing.Result = j.Result.CloneResult()
	}
	existing.RawLogPath = j.RawLogPath
	existing.Tokens = j.Tokens
	if j.EventJSON != "" {
		existing.EventJSON = j.EventJSON
	}

	return nil
}

func (s *MemoryStore) GetJob(id string) (*job.ActionJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.m[id]
	if !ok {
		// Match SQLiteStore semantics: not-found returns an error so HTTP
		// handlers (which check err != nil) respond 404 rather than nil-deref.
		return nil, fmt.Errorf("job not found: %s", id)
	}
	return j.Clone(), nil
}

func (s *MemoryStore) ListJobs(limit int) ([]*job.ActionJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > len(s.jobs) {
		limit = len(s.jobs)
	}
	// Return copies so readers never share state with the worker goroutine.
	res := make([]*job.ActionJob, limit)
	for i := 0; i < limit; i++ {
		res[i] = s.jobs[i].Clone()
	}
	return res, nil
}

func (s *MemoryStore) ListJobsByEventID(eventID string) ([]*job.ActionJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var res []*job.ActionJob
	for _, j := range s.jobs {
		if j.EventID == eventID {
			res = append(res, j.Clone())
		}
	}
	return res, nil
}

// ListRecoverableJobs returns non-terminal jobs created at/after `since`,
// for re-queueing after a restart (ASSURANCE §7 前置债).
func (s *MemoryStore) ListRecoverableJobs(since time.Time) ([]*job.ActionJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var res []*job.ActionJob
	for _, j := range s.jobs {
		if isTerminalStatus(j.Status) {
			continue
		}
		if j.CreatedAt.Before(since) {
			continue
		}
		res = append(res, j.Clone())
	}
	return res, nil
}

// AbandonStaleJobs marks non-terminal jobs older than the cutoff as
// cancelled (ASSURANCE §7: 僵尸任务清理).
func (s *MemoryStore) AbandonStaleJobs(cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	now := time.Now()
	for _, j := range s.jobs {
		if isTerminalStatus(j.Status) || !j.CreatedAt.Before(cutoff) {
			continue
		}
		j.Status = job.StatusCanceled
		j.ErrorSummary = "abandoned: stale before recovery window"
		j.CompletedAt = now
		n++
	}
	return n, nil
}

// DeleteJob removes a single job from the in-memory store.
func (s *MemoryStore) DeleteJob(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.m[id]; !ok {
		return nil
	}
	delete(s.m, id)
	for i, j := range s.jobs {
		if j.ID == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			break
		}
	}
	return nil
}

// DeleteJobsBefore removes terminal jobs created before the cutoff and
// returns how many were deleted.
func (s *MemoryStore) DeleteJobsBefore(cutoff time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	deleted := 0
	keep := make([]*job.ActionJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		if j.CreatedAt.Before(cutoff) && isTerminalStatus(j.Status) {
			delete(s.m, j.ID)
			deleted++
			continue
		}
		keep = append(keep, j)
	}
	s.jobs = keep
	return deleted, nil
}

// cloneTimePtr returns a copy of a *time.Time (nil-safe) so store snapshots
// never share time pointers with the caller's live job object.
func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}
