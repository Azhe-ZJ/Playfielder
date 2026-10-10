package store

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Azhe-ZJ/Playfielder/internal/model"
)

func pkg() model.Package {
	return model.Package{Format: "playfielder", Version: 1, Title: "Test", Content: model.Content{Source: model.Source{HTML: "<h1>Original</h1>"}, Story: model.EmptyStory()}, Assets: []model.Asset{}}
}
func openTest(t *testing.T) *Store {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestConcurrentSaveAndUndo(t *testing.T) {
	s := openTest(t)
	p, e := s.Create(pkg())
	if e != nil {
		t.Fatal(e)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := p.Content
			c.Source.HTML = "<h1>Changed</h1>"
			_, e := s.Save(p.ID, p.Revision, p.Title, c)
			if e == nil {
				successes.Add(1)
			} else if !errors.Is(e, model.ErrConflict) {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("stale writes were not rejected")
	}
	saved, _ := s.Get(p.ID)
	back, e := s.Undo(saved.ID, saved.Revision, false)
	if e != nil || !model.Equal(back.Content, p.Content) {
		t.Fatal("undo failed", e)
	}
	forward, e := s.Undo(back.ID, back.Revision, true)
	if e != nil || forward.Content.Source.HTML != saved.Content.Source.HTML {
		t.Fatal("redo failed", e)
	}
}
func TestReopenAndRecovery(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.Create(pkg())
	if e != nil {
		t.Fatal(e)
	}
	task, created, e := s.CreateTask(p.ID, p.Revision, "request", "Change it", "fingerprint")
	if e != nil || !created {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.RecoverTasks(); e != nil {
		t.Fatal(e)
	}
	recovered, e := s.Task(task.ID)
	if e != nil || recovered.State != "interrupted" {
		t.Fatal(recovered, e)
	}
	project, e := s.Get(p.ID)
	if e != nil || !model.Equal(project.Content, p.Content) {
		t.Fatal("project not preserved")
	}
	if _, _, e = s.CreateTask(p.ID, p.Revision, "request", "Different", "fingerprint"); e == nil {
		t.Fatal("request ID reused with different content")
	}
}
func TestAtomicGenerationAndCancel(t *testing.T) {
	s := openTest(t)
	p, _ := s.Create(pkg())
	task, _, _ := s.CreateTask(p.ID, p.Revision, "r1", "Change", "fp")
	c := p.Content
	c.Source.HTML = "<h1>Generated</h1>"
	out, e := s.CommitTask(task.ID, c)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.CommitTask(task.ID, c)
	if e != nil || len(again.Versions) != 2 || again.Revision != out.Revision {
		t.Fatal("duplicate commit")
	}
	task, _, _ = s.CreateTask(out.ID, out.Revision, "r2", "More", "fp")
	_, e = s.UpdateTask(task.ID, func(t *model.Task) error { t.State = "canceled"; return nil })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CommitTask(task.ID, c); e == nil {
		t.Fatal("canceled task committed")
	}
	task, _, _ = s.CreateTask(out.ID, out.Revision, "r3", "Another", "fp")
	edited := out.Content
	edited.Source.HTML = "<p>Human changes</p>"
	_, _ = s.Save(out.ID, out.Revision, out.Title, edited)
	if _, e = s.CommitTask(task.ID, c); !errors.Is(e, model.ErrConflict) {
		t.Fatal("generation overwrote human changes")
	}
}
func TestTwoProcessesAndIndependentProjects(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	second, e := Open(dir)
	if e == nil {
		second.Close()
		t.Fatal("allowed second database writer")
	}
	a, _ := s.Create(pkg())
	b, _ := s.Create(pkg())
	if a.ID == b.ID {
		t.Fatal("same project ID")
	}
	if _, e = s.Get("../outside"); !errors.Is(e, model.ErrNotFound) {
		t.Fatal("invalid ID accepted")
	}
}
