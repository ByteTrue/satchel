package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

const (
	maxActiveGenerations = 8
	generationResultTTL  = time.Hour
)

type generationResult = GenerationResult

type generationStart struct {
	ID      string
	Pending bool
	Result  generationResult
}

type generationRun func(context.Context) (generationResult, error)

type generationJob struct {
	done       chan struct{}
	cancel     context.CancelFunc
	result     generationResult
	err        error
	finishedAt time.Time
}

type generationManager struct {
	ctx    context.Context
	cancel context.CancelFunc
	wait   time.Duration

	mu     sync.Mutex
	jobs   map[string]*generationJob
	active int
	now    func() time.Time
}

func newGenerationManager(parent context.Context, wait time.Duration) *generationManager {
	ctx, cancel := context.WithCancel(parent)
	return &generationManager{
		ctx: ctx, cancel: cancel, wait: wait,
		jobs: make(map[string]*generationJob), now: time.Now,
	}
}

func (m *generationManager) Start(ctx context.Context, run generationRun) (generationStart, error) {
	m.mu.Lock()
	m.pruneLocked()
	if m.active >= maxActiveGenerations {
		m.mu.Unlock()
		return generationStart{}, errors.New("too many generation tasks are already running; collect a pending result or try again later")
	}
	id, err := generationID()
	if err != nil {
		m.mu.Unlock()
		return generationStart{}, err
	}
	jobCtx, cancel := context.WithCancel(m.ctx)
	job := &generationJob{done: make(chan struct{}), cancel: cancel}
	m.jobs[id] = job
	m.active++
	m.mu.Unlock()

	go func() {
		defer close(job.done)
		defer cancel()
		job.result, job.err = run(jobCtx)
		m.mu.Lock()
		m.active--
		job.finishedAt = m.now()
		m.mu.Unlock()
	}()

	timer := time.NewTimer(m.wait)
	defer timer.Stop()
	select {
	case <-job.done:
		m.mu.Lock()
		delete(m.jobs, id)
		m.mu.Unlock()
		return generationStart{Result: job.result}, job.err
	case <-timer.C:
		return generationStart{ID: id, Pending: true}, nil
	case <-ctx.Done():
		job.cancel()
		m.mu.Lock()
		delete(m.jobs, id)
		m.mu.Unlock()
		return generationStart{}, ctx.Err()
	}
}

func (m *generationManager) Wait(ctx context.Context, id string) (generationResult, bool, error) {
	m.mu.Lock()
	m.pruneLocked()
	job := m.jobs[id]
	m.mu.Unlock()
	if job == nil {
		return generationResult{}, false, errors.New("unknown or expired generation task " + id)
	}

	timer := time.NewTimer(m.wait)
	defer timer.Stop()
	select {
	case <-job.done:
		return job.result, false, job.err
	case <-timer.C:
		return generationResult{}, true, nil
	case <-ctx.Done():
		return generationResult{}, false, ctx.Err()
	}
}

func (m *generationManager) Close() { m.cancel() }

func (m *generationManager) pruneLocked() {
	for id, job := range m.jobs {
		if !job.finishedAt.IsZero() && m.now().Sub(job.finishedAt) > generationResultTTL {
			delete(m.jobs, id)
		}
	}
}

func generationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
