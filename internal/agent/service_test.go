package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azhe-ZJ/Playfielder/internal/model"
	"github.com/Azhe-ZJ/Playfielder/internal/store"
)

type fakeProvider struct {
	calls   atomic.Int32
	wait    bool
	entered chan struct{}
}

func (f *fakeProvider) Fingerprint() string { return "test-provider" }
func (f *fakeProvider) Configured() bool    { return true }
func (f *fakeProvider) Generate(ctx context.Context, t model.Task, progress func(string) error) (model.Content, model.Usage, error) {
	f.calls.Add(1)
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.wait {
		<-ctx.Done()
		return model.Content{}, model.Usage{}, ctx.Err()
	}
	c := t.BaseContent
	c.Source.HTML = "<h1>AI result</h1>"
	return c, model.Usage{Calls: 1, InputTokens: 20, OutputTokens: 30}, nil
}
func setup(t *testing.T, p Provider) (*store.Store, *Engine, model.Project) {
	t.Helper()
	s, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	engine, e := New(s, p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { engine.Close(); s.Close() })
	project, e := s.Create(model.Package{Format: "playfielder", Version: 1, Title: "Test", Content: model.Content{Source: model.Source{HTML: "<h1>Original</h1>"}, Story: model.EmptyStory()}, Assets: []model.Asset{}})
	if e != nil {
		t.Fatal(e)
	}
	return s, engine, project
}
func waitTask(t *testing.T, s *store.Store, id string) model.Task {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, e := s.Task(id)
		if e != nil {
			t.Fatal(e)
		}
		if !model.Active(task.State) {
			return task
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("task did not finish")
	return model.Task{}
}
func TestGenerationIdempotencyAndUsage(t *testing.T) {
	provider := &fakeProvider{}
	s, e, p := setup(t, provider)
	task, err := e.Start(p.ID, p.Revision, "request", "Make story")
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.Start(p.ID, p.Revision, "request", "Make story")
	if err != nil || again.ID != task.ID {
		t.Fatal("duplicate request", err)
	}
	done := waitTask(t, s, task.ID)
	if done.State != "completed" || provider.calls.Load() != 1 || done.Usage.InputTokens != 20 {
		t.Fatal(done)
	}
	out, _ := s.Get(p.ID)
	if len(out.Versions) != 2 || !strings.Contains(out.Content.Source.HTML, "AI result") {
		t.Fatal("no atomic version")
	}
}
func TestCancelCannotCommitAndCloseInterrupts(t *testing.T) {
	provider := &fakeProvider{wait: true, entered: make(chan struct{}, 2)}
	s, e, p := setup(t, provider)
	task, err := e.Start(p.ID, p.Revision, "r1", "Make story")
	if err != nil {
		t.Fatal(err)
	}
	<-provider.entered
	if _, err = e.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	done := waitTask(t, s, task.ID)
	if done.State != "canceled" {
		t.Fatal(done)
	}
	out, _ := s.Get(p.ID)
	if len(out.Versions) != 1 || out.Revision != p.Revision {
		t.Fatal("cancel changed project")
	}
	second, err := e.Start(p.ID, p.Revision, "r2", "Make story")
	if err != nil {
		t.Fatal(err)
	}
	<-provider.entered
	e.Close()
	interrupted, _ := s.Task(second.ID)
	if interrupted.State != "interrupted" {
		t.Fatal(interrupted)
	}
}
func TestCheckpointResumeAvoidsAnotherBillableCall(t *testing.T) {
	provider := &fakeProvider{}
	s, e, p := setup(t, provider)
	task, _, err := s.CreateTask(p.ID, p.Revision, "request", "Make story", provider.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	c := p.Content
	c.Source.HTML = "<h1>Checkpoint</h1>"
	_, err = s.UpdateTask(task.ID, func(t *model.Task) error { t.Checkpoint = &c; t.State = "interrupted"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Resume(task.ID); err != nil {
		t.Fatal(err)
	}
	done := waitTask(t, s, task.ID)
	if done.State != "completed" || provider.calls.Load() != 0 {
		t.Fatal("checkpoint repeated model request", done)
	}
}
func TestCompatibleStreamingAndNoRawSecretErrors(t *testing.T) {
	c := model.Content{Source: model.Source{HTML: "<h1>Streamed</h1>"}, Story: model.EmptyStory()}
	content, _ := json.Marshal(c)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("request contract")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, part := range []string{string(content[:30]), string(content[30:])} {
			event, _ := json.Marshal(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"delta": map[string]string{"content": part}}}})
			fmt.Fprintf(w, "data: %s\n\n", event)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":20}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	provider := &Remote{BaseURL: server.URL + "/v1", APIKey: "synthetic", Model: "fixture"}
	var progress string
	out, usage, err := provider.Generate(context.Background(), model.Task{Prompt: "test", BaseContent: c}, func(s string) error { progress = s; return nil })
	if err != nil || out.Source.HTML != c.Source.HTML || usage.OutputTokens != 20 || progress != string(content) {
		t.Fatal(out, usage, err)
	}
}
func TestIncompleteAndUnauthorizedNotRetriedBlindly(t *testing.T) {
	for _, mode := range []string{"truncated", "unauthorized", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "unauthorized":
					w.WriteHeader(401)
					fmt.Fprint(w, "private-secret-from-provider")
				case "redirect":
					w.Header().Set("Location", "https://example.com")
					w.WriteHeader(302)
				default:
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\n")
				}
			}))
			defer server.Close()
			p := &Remote{BaseURL: server.URL, APIKey: "synthetic", Model: "fixture"}
			_, _, err := p.Generate(context.Background(), model.Task{}, func(string) error { return nil })
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
			failure, ok := err.(*Failure)
			if !ok || failure.Retryable {
				t.Fatal("nonretryable failure classified incorrectly", err)
			}
		})
	}
}
