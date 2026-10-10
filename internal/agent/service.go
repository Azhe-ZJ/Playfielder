// Package agent owns the program-controlled generation lifecycle. Jobs are
// durable and explicitly resumed; restarting never silently spends model credit.
package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Azhe-ZJ/Playfielder/internal/document"
	"github.com/Azhe-ZJ/Playfielder/internal/model"
	"github.com/Azhe-ZJ/Playfielder/internal/store"
)

type Engine struct {
	store    *store.Store
	provider Provider
	mu       sync.Mutex
	running  map[string]context.CancelFunc
	slots    chan struct{}
	wg       sync.WaitGroup
	closing  bool
}

func New(s *store.Store, p Provider) (*Engine, error) {
	if e := s.RecoverTasks(); e != nil {
		return nil, e
	}
	return &Engine{store: s, provider: p, running: map[string]context.CancelFunc{}, slots: make(chan struct{}, 2)}, nil
}
func (e *Engine) Configured() bool { return e.provider != nil && e.provider.Configured() }
func (e *Engine) Start(projectID, revision, requestID, prompt string) (model.Task, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closing {
		return model.Task{}, errors.New("server_shutting_down")
	}
	if !e.Configured() {
		return model.Task{}, errors.New("ai_not_configured")
	}
	t, created, err := e.store.CreateTask(projectID, revision, requestID, prompt, e.provider.Fingerprint())
	if err != nil {
		return t, err
	}
	if created {
		e.launch(t)
	}
	return t, nil
}
func (e *Engine) Resume(id string) (model.Task, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closing {
		return model.Task{}, errors.New("server_shutting_down")
	}
	if !e.Configured() {
		return model.Task{}, errors.New("ai_not_configured")
	}
	t, err := e.store.ResumeTask(id, e.provider.Fingerprint())
	if err == nil {
		e.launch(t)
	}
	return t, err
}
func (e *Engine) Cancel(id string) (model.Task, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.store.UpdateTask(id, func(t *model.Task) error {
		if model.Active(t.State) || t.State == "interrupted" || t.State == "failed" {
			t.State = "canceled"
			t.Output = ""
			t.Error = "author_canceled"
		}
		return nil
	})
	if cancel := e.running[id]; cancel != nil {
		cancel()
	}
	return t, err
}
func (e *Engine) Close() {
	e.mu.Lock()
	e.closing = true
	for _, cancel := range e.running {
		cancel()
	}
	e.mu.Unlock()
	e.wg.Wait()
}
func (e *Engine) launch(t model.Task) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	e.running[t.ID] = cancel
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer cancel()
		defer func() { e.mu.Lock(); delete(e.running, t.ID); e.mu.Unlock() }()
		e.run(ctx, t)
	}()
}
func (e *Engine) fail(id string, err error) {
	e.mu.Lock()
	closing := e.closing
	e.mu.Unlock()
	_, _ = e.store.UpdateTask(id, func(t *model.Task) error {
		if !model.Active(t.State) {
			return nil
		}
		t.State = "failed"
		t.Error = "generation_failed"
		t.Output = ""
		var failure *Failure
		if errors.As(err, &failure) {
			t.Error = failure.Code
		} else if errors.Is(err, model.ErrConflict) {
			t.Error = "generation_conflict"
		} else if errors.Is(err, model.ErrBudget) {
			t.Error = "generation_budget_exhausted"
		} else if errors.Is(err, context.Canceled) {
			if closing {
				t.State = "interrupted"
				t.Error = "server_stopped"
			} else {
				t.State = "canceled"
				t.Error = "author_canceled"
			}
		} else if errors.Is(err, context.DeadlineExceeded) {
			t.Error = "generation_timeout"
		} else if err.Error() == "javascript_syntax_invalid" {
			t.Error = "javascript_syntax_invalid"
		}
		return nil
	})
}
func (e *Engine) run(ctx context.Context, t model.Task) {
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	case <-ctx.Done():
		e.fail(t.ID, ctx.Err())
		return
	}
	var err error
	t, err = e.store.UpdateTask(t.ID, func(t *model.Task) error {
		if !model.Active(t.State) {
			return errors.New("task_not_active")
		}
		t.State = "running"
		return nil
	})
	if err != nil {
		return
	}
	var content model.Content
	if t.Checkpoint != nil {
		content = *t.Checkpoint
	} else {
		for attempt := 0; attempt < 2; attempt++ {
			if ctx.Err() != nil {
				e.fail(t.ID, ctx.Err())
				return
			}
			t, err = e.store.UpdateTask(t.ID, func(t *model.Task) error {
				if !model.Active(t.State) {
					return context.Canceled
				}
				if t.Attempts >= 4 {
					return model.ErrBudget
				}
				t.Attempts++
				t.Usage.Calls++
				t.Phase = "generate"
				t.Output = ""
				return nil
			})
			if err != nil {
				e.fail(t.ID, err)
				return
			}
			last := time.Time{}
			var usage model.Usage
			content, usage, err = e.provider.Generate(ctx, t, func(text string) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if time.Since(last) < 350*time.Millisecond {
					return nil
				}
				last = time.Now()
				_, updateErr := e.store.UpdateTask(t.ID, func(saved *model.Task) error {
					if !model.Active(saved.State) {
						return context.Canceled
					}
					saved.Output = text
					return nil
				})
				return updateErr
			})
			_, saveErr := e.store.UpdateTask(t.ID, func(saved *model.Task) error {
				saved.Usage.InputTokens += usage.InputTokens
				saved.Usage.OutputTokens += usage.OutputTokens
				return nil
			})
			if saveErr != nil {
				e.fail(t.ID, saveErr)
				return
			}
			if err == nil {
				break
			}
			var failure *Failure
			if !errors.As(err, &failure) || !failure.Retryable || attempt == 1 {
				e.fail(t.ID, err)
				return
			}
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				e.fail(t.ID, ctx.Err())
				return
			}
		}
		content.Source, err = document.Prepare(content.Source)
		if err != nil {
			e.fail(t.ID, err)
			return
		}
		_, err = e.store.UpdateTask(t.ID, func(saved *model.Task) error {
			if !model.Active(saved.State) {
				return context.Canceled
			}
			saved.Checkpoint = &content
			saved.Phase = "validate"
			saved.Output = ""
			return nil
		})
		if err != nil {
			e.fail(t.ID, err)
			return
		}
	}
	if ctx.Err() != nil {
		e.fail(t.ID, ctx.Err())
		return
	}
	p, err := e.store.Get(t.ProjectID)
	if err != nil {
		e.fail(t.ID, err)
		return
	}
	if _, err = document.Build(document.Options{Title: p.Title, Content: content, Assets: p.Assets}); err != nil {
		e.fail(t.ID, err)
		return
	}
	if ctx.Err() != nil {
		e.fail(t.ID, ctx.Err())
		return
	}
	if _, err = e.store.CommitTask(t.ID, content); err != nil {
		e.fail(t.ID, err)
	}
}
