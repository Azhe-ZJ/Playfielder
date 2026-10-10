package browser

import (
	"context"
	"os"
	"testing"

	"github.com/Azhe-ZJ/Playfielder/internal/document"
	"github.com/Azhe-ZJ/Playfielder/internal/model"
)

func TestIsolatedBrowserRuntime(t *testing.T) {
	if os.Getenv("PLAYFIELDER_BROWSER_TEST") != "1" {
		t.Skip("explicit browser acceptance only")
	}
	for _, flow := range []model.Story{model.EmptyStory(), {Version: 1, Characters: []model.Character{{ID: "n", Name: "Narrator"}}, Nodes: []model.StoryNode{{ID: "hello", CharacterID: "n", Text: "Hello", Event: "start", Replies: []model.Reply{}}}}} {
		checks, err := CheckContent(context.Background(), document.Options{Title: "Acceptance", Content: model.Content{Source: model.Source{HTML: "<main><h1>Hello</h1></main>"}, Story: flow}})
		if err != nil {
			t.Fatal(err)
		}
		if len(checks) != 2 {
			t.Fatal(checks)
		}
		for _, check := range checks {
			if !check.Ready || !check.Isolated || len(check.Errors) > 0 {
				t.Fatalf("runtime check: %+v", check)
			}
		}
	}
}
