// Package store owns all durable mutations. One bbolt writer atomically commits
// content, revisions and generation receipts; the database also locks out a
// second process attempting to use the same data directory.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Azhe-ZJ/Playfielder/internal/model"
	"github.com/Azhe-ZJ/Playfielder/internal/story"
	bolt "go.etcd.io/bbolt"
)

var projectsBucket = []byte("projects")
var tasksBucket = []byte("tasks")
var statesBucket = []byte("player-states")
var publicationsBucket = []byte("publications")

type Store struct{ db *bolt.DB }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "playfielder.db")
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open independent data directory: %w", err)
	}
	s := &Store{db: db}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{projectsBucket, tasksBucket, statesBucket, publicationsBucket} {
			if _, e := tx.CreateBucketIfNotExists(name); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func read[T any](b *bolt.Bucket, id string) (T, error) {
	var v T
	if !model.ValidID(id) {
		return v, model.ErrNotFound
	}
	data := b.Get([]byte(id))
	if data == nil {
		return v, model.ErrNotFound
	}
	err := json.Unmarshal(data, &v)
	return v, err
}
func put(b *bolt.Bucket, id string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return b.Put([]byte(id), data)
}
func (s *Store) Get(id string) (model.Project, error) {
	var p model.Project
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		p, e = read[model.Project](tx.Bucket(projectsBucket), id)
		return e
	})
	return p, err
}
func (s *Store) List() ([]model.ProjectSummary, error) {
	out := []model.ProjectSummary{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(projectsBucket).ForEach(func(_, v []byte) error {
			var p model.Project
			if e := json.Unmarshal(v, &p); e != nil {
				return e
			}
			out = append(out, model.ProjectSummary{ID: p.ID, Title: p.Title, Revision: p.Revision, UpdatedAt: p.UpdatedAt, Versions: len(p.Versions), Assets: len(p.Assets)})
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, err
}
func (s *Store) Create(pkg model.Package) (model.Project, error) {
	if err := pkg.Validate(); err != nil {
		return model.Project{}, err
	}
	now := model.Now()
	p := model.Project{ID: model.NewID(), Title: pkg.Title, Revision: model.NewID(), CreatedAt: now, UpdatedAt: now, Content: pkg.Content, Assets: pkg.Assets, Undo: []model.Content{}, Redo: []model.Content{}, Versions: []model.Version{}}
	if p.Assets == nil {
		p.Assets = []model.Asset{}
	}
	p.Versions = append(p.Versions, model.Version{ID: model.NewID(), Number: 1, CreatedAt: now, Summary: "Initial version", Content: p.Content})
	err := s.db.Update(func(tx *bolt.Tx) error { return put(tx.Bucket(projectsBucket), p.ID, p) })
	return p, err
}
func (s *Store) mutate(id, revision string, fn func(*model.Project) error) (model.Project, error) {
	var out model.Project
	err := s.db.Update(func(tx *bolt.Tx) error {
		p, e := read[model.Project](tx.Bucket(projectsBucket), id)
		if e != nil {
			return e
		}
		if revision == "" || p.Revision != revision {
			return model.ErrConflict
		}
		if e = fn(&p); e != nil {
			return e
		}
		p.Revision = model.NewID()
		p.UpdatedAt = model.Now()
		if e = put(tx.Bucket(projectsBucket), id, p); e != nil {
			return e
		}
		out = p
		return nil
	})
	return out, err
}
func trim[T any](xs []T, max int) []T {
	if len(xs) > max {
		return xs[len(xs)-max:]
	}
	return xs
}
func pushContent(p *model.Project, c model.Content) {
	if !model.Equal(p.Content, c) {
		p.Undo = trim(append(p.Undo, p.Content), 12)
		p.Redo = []model.Content{}
		p.Content = c
	}
}
func (s *Store) Save(id, revision, title string, c model.Content) (model.Project, error) {
	if strings.TrimSpace(title) == "" || len(title) > 240 {
		return model.Project{}, errors.New("invalid_title")
	}
	if err := c.Validate(); err != nil {
		return model.Project{}, err
	}
	return s.mutate(id, revision, func(p *model.Project) error { p.Title = title; pushContent(p, c); return nil })
}
func (s *Store) Undo(id, revision string, redo bool) (model.Project, error) {
	return s.mutate(id, revision, func(p *model.Project) error {
		if redo {
			if len(p.Redo) == 0 {
				return errors.New("nothing_to_redo")
			}
			p.Undo = trim(append(p.Undo, p.Content), 12)
			p.Content = p.Redo[len(p.Redo)-1]
			p.Redo = p.Redo[:len(p.Redo)-1]
		} else {
			if len(p.Undo) == 0 {
				return errors.New("nothing_to_undo")
			}
			p.Redo = trim(append(p.Redo, p.Content), 12)
			p.Content = p.Undo[len(p.Undo)-1]
			p.Undo = p.Undo[:len(p.Undo)-1]
		}
		return nil
	})
}
func addVersion(p *model.Project, summary, taskID string) {
	number := 1
	if len(p.Versions) > 0 {
		number = p.Versions[len(p.Versions)-1].Number + 1
	}
	p.Versions = trim(append(p.Versions, model.Version{ID: model.NewID(), Number: number, CreatedAt: model.Now(), Summary: summary, TaskID: taskID, Content: p.Content}), 100)
}
func (s *Store) Snapshot(id, revision, summary string) (model.Project, error) {
	if len(summary) > 1000 {
		return model.Project{}, errors.New("summary_too_large")
	}
	return s.mutate(id, revision, func(p *model.Project) error { addVersion(p, summary, ""); return nil })
}
func (s *Store) Restore(id, revision, versionID string) (model.Project, error) {
	return s.mutate(id, revision, func(p *model.Project) error {
		for _, v := range p.Versions {
			if v.ID == versionID {
				pushContent(p, v.Content)
				return nil
			}
		}
		return model.ErrNotFound
	})
}
func (s *Store) AddAsset(id, revision string, a model.Asset) (model.Project, error) {
	if err := a.Validate(); err != nil {
		return model.Project{}, err
	}
	return s.mutate(id, revision, func(p *model.Project) error {
		if len(p.Assets) >= model.MaxAssets {
			return errors.New("too_many_assets")
		}
		total := len(a.Data)
		for _, old := range p.Assets {
			if old.ID == a.ID {
				return errors.New("duplicate_asset")
			}
			total += len(old.Data)
		}
		if total > model.MaxPackageBytes {
			return errors.New("assets_too_large")
		}
		p.Assets = append(p.Assets, a)
		return nil
	})
}
func (s *Store) Task(id string) (model.Task, error) {
	var t model.Task
	err := s.db.View(func(tx *bolt.Tx) error { var e error; t, e = read[model.Task](tx.Bucket(tasksBucket), id); return e })
	return t, err
}
func (s *Store) Tasks(projectID string) ([]model.Task, error) {
	out := []model.Task{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(tasksBucket).ForEach(func(_, v []byte) error {
			var t model.Task
			if e := json.Unmarshal(v, &t); e != nil {
				return e
			}
			if t.ProjectID == projectID {
				out = append(out, t)
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, err
}
func activeIn(b *bolt.Bucket, projectID, except string) error {
	return b.ForEach(func(_, v []byte) error {
		var t model.Task
		if e := json.Unmarshal(v, &t); e != nil {
			return e
		}
		if t.ProjectID == projectID && t.ID != except && model.Active(t.State) {
			return model.ErrBusy
		}
		return nil
	})
}
func (s *Store) CreateTask(projectID, revision, requestID, prompt, fingerprint string) (model.Task, bool, error) {
	var out model.Task
	created := false
	if !model.ValidID(requestID) || strings.TrimSpace(prompt) == "" || len(prompt) > 24000 {
		return out, false, errors.New("invalid_generation_request")
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(tasksBucket)
		var duplicate *model.Task
		if e := b.ForEach(func(_, v []byte) error {
			var t model.Task
			if e := json.Unmarshal(v, &t); e != nil {
				return e
			}
			if t.ProjectID == projectID && t.RequestID == requestID {
				duplicate = &t
			}
			return nil
		}); e != nil {
			return e
		}
		if duplicate != nil {
			if duplicate.Prompt != prompt || duplicate.BaseRevision != revision {
				return errors.New("request_id_reused")
			}
			out = *duplicate
			return nil
		}
		p, e := read[model.Project](tx.Bucket(projectsBucket), projectID)
		if e != nil {
			return e
		}
		if p.Revision != revision {
			return model.ErrConflict
		}
		if e := activeIn(b, projectID, ""); e != nil {
			return e
		}
		now := model.Now()
		out = model.Task{ID: model.NewID(), ProjectID: projectID, RequestID: requestID, Prompt: prompt, BaseRevision: revision, BaseContent: p.Content, State: "queued", Phase: "generate", ProviderFingerprint: fingerprint, CreatedAt: now, UpdatedAt: now}
		created = true
		return put(b, out.ID, out)
	})
	return out, created, err
}
func (s *Store) UpdateTask(id string, fn func(*model.Task) error) (model.Task, error) {
	var out model.Task
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(tasksBucket)
		t, e := read[model.Task](b, id)
		if e != nil {
			return e
		}
		if e = fn(&t); e != nil {
			return e
		}
		t.UpdatedAt = model.Now()
		if e = put(b, id, t); e != nil {
			return e
		}
		out = t
		return nil
	})
	return out, err
}
func (s *Store) ResumeTask(id, fingerprint string) (model.Task, error) {
	var out model.Task
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(tasksBucket)
		t, e := read[model.Task](b, id)
		if e != nil {
			return e
		}
		if t.State != "interrupted" && t.State != "failed" {
			return errors.New("task_not_resumable")
		}
		if t.Error == "generation_conflict" {
			return model.ErrConflict
		}
		if t.ProviderFingerprint != fingerprint {
			return errors.New("provider_changed_start_new_task")
		}
		if t.Resumes >= 2 || t.Attempts >= 4 {
			return model.ErrBudget
		}
		p, e := read[model.Project](tx.Bucket(projectsBucket), t.ProjectID)
		if e != nil {
			return e
		}
		if p.Revision != t.BaseRevision {
			return model.ErrConflict
		}
		if e = activeIn(b, t.ProjectID, t.ID); e != nil {
			return e
		}
		t.Resumes++
		t.State = "queued"
		t.Error = ""
		t.Output = ""
		t.UpdatedAt = model.Now()
		if e = put(b, t.ID, t); e != nil {
			return e
		}
		out = t
		return nil
	})
	return out, err
}
func (s *Store) RecoverTasks() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(tasksBucket)
		changes := []model.Task{}
		if e := b.ForEach(func(_, v []byte) error {
			var t model.Task
			if e := json.Unmarshal(v, &t); e != nil {
				return e
			}
			if model.Active(t.State) {
				t.State = "interrupted"
				t.Error = "server_restarted"
				t.UpdatedAt = model.Now()
				changes = append(changes, t)
			}
			return nil
		}); e != nil {
			return e
		}
		for _, t := range changes {
			if e := put(b, t.ID, t); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) CommitTask(id string, c model.Content) (model.Project, error) {
	if e := c.Validate(); e != nil {
		return model.Project{}, e
	}
	var out model.Project
	err := s.db.Update(func(tx *bolt.Tx) error {
		tb := tx.Bucket(tasksBucket)
		t, e := read[model.Task](tb, id)
		if e != nil {
			return e
		}
		pb := tx.Bucket(projectsBucket)
		p, e := read[model.Project](pb, t.ProjectID)
		if e != nil {
			return e
		}
		if t.State == "completed" {
			out = p
			return nil
		}
		if !model.Active(t.State) {
			return errors.New("task_not_active")
		}
		if p.Revision != t.BaseRevision {
			return model.ErrConflict
		}
		pushContent(&p, c)
		addVersion(&p, "AI: "+t.Prompt[:min(len(t.Prompt), 200)], id)
		p.Revision = model.NewID()
		p.UpdatedAt = model.Now()
		t.State = "completed"
		t.Phase = "complete"
		t.Checkpoint = &c
		t.Output = ""
		t.Error = ""
		t.UpdatedAt = model.Now()
		if e = put(pb, p.ID, p); e != nil {
			return e
		}
		if e = put(tb, id, t); e != nil {
			return e
		}
		out = p
		return nil
	})
	return out, err
}
func (s *Store) ApplyStory(id string, a story.Action, now int64) (story.State, error) {
	var out story.State
	err := s.db.Update(func(tx *bolt.Tx) error {
		p, e := read[model.Project](tx.Bucket(projectsBucket), id)
		if e != nil {
			return e
		}
		b := tx.Bucket(statesBucket)
		state, e := read[story.State](b, id)
		if errors.Is(e, model.ErrNotFound) {
			state = story.Empty()
		} else if e != nil {
			return e
		}
		out, e = story.Step(p.Content.Story, state, a, now)
		if e != nil {
			return e
		}
		return put(b, id, out)
	})
	return out, err
}
func (s *Store) ResetStory(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if _, e := read[model.Project](tx.Bucket(projectsBucket), id); e != nil {
			return e
		}
		return put(tx.Bucket(statesBucket), id, story.Empty())
	})
}

type Publication struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	Content   model.Content `json:"content"`
	Assets    []model.Asset `json:"assets"`
	CreatedAt time.Time     `json:"createdAt"`
}

func (s *Store) Publish(id, revision string) (Publication, error) {
	var out Publication
	err := s.db.Update(func(tx *bolt.Tx) error {
		p, e := read[model.Project](tx.Bucket(projectsBucket), id)
		if e != nil {
			return e
		}
		if p.Revision != revision {
			return model.ErrConflict
		}
		out = Publication{ID: model.NewID(), Title: p.Title, Content: p.Content, Assets: p.Assets, CreatedAt: model.Now()}
		return put(tx.Bucket(publicationsBucket), out.ID, out)
	})
	return out, err
}
func (s *Store) Publication(id string) (Publication, error) {
	var out Publication
	err := s.db.View(func(tx *bolt.Tx) error {
		var e error
		out, e = read[Publication](tx.Bucket(publicationsBucket), id)
		return e
	})
	return out, err
}
