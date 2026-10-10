package story

import (
	"reflect"
	"testing"

	"github.com/Azhe-ZJ/Playfielder/internal/model"
)

func fixture() model.Story {
	return model.Story{Version: 1, Characters: []model.Character{{ID: "guide", Name: "Guide"}}, Nodes: []model.StoryNode{{ID: "a", CharacterID: "guide", Text: "First", Event: "start", DelayMS: 500, Replies: []model.Reply{{ID: "r", Text: "Open", NextID: "b", Requires: []string{"key"}, Effects: []model.Effect{{Type: "unlock", Target: "door"}}}}}, {ID: "b", CharacterID: "guide", Text: "Second", DelayMS: 100, Replies: []model.Reply{}}}}
}
func TestDelayedReplyRequiresAndExactlyOnce(t *testing.T) {
	f := fixture()
	s, e := Step(f, Empty(), Action{Type: "event", Event: "start"}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Messages) != 0 || len(s.Pending) != 1 {
		t.Fatal("delay not scheduled")
	}
	original := s
	s, e = Step(f, s, Action{Type: "tick"}, 1500)
	if e != nil {
		t.Fatal(e)
	}
	if len(original.Messages) != 0 {
		t.Fatal("mutated previous state")
	}
	if len(s.Messages) != 1 || len(Available(f, s, "a")) != 0 {
		t.Fatal("delivery/requirements")
	}
	if _, e = Step(f, s, Action{Type: "reply", NodeID: "a", ReplyID: "r"}, 1500); e == nil {
		t.Fatal("allowed unavailable reply")
	}
	s, e = Step(f, s, Action{Type: "event", Event: "key"}, 1600)
	if e != nil {
		t.Fatal(e)
	}
	s, e = Step(f, s, Action{Type: "reply", NodeID: "a", ReplyID: "r"}, 1600)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(s.Unlocked, []string{"door"}) {
		t.Fatal(s)
	}
	if _, e = Step(f, s, Action{Type: "reply", NodeID: "a", ReplyID: "r"}, 1600); e == nil {
		t.Fatal("duplicate reply accepted")
	}
	s, e = Step(f, s, Action{Type: "tick"}, 1700)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Messages) != 3 || len(s.Pending) != 0 {
		t.Fatal(s)
	}
	again, e := Step(f, s, Action{Type: "event", Event: "start"}, 5000)
	if e != nil || len(again.Messages) != 3 {
		t.Fatal("duplicate start delivery")
	}
}
func TestInvalidGraphAndState(t *testing.T) {
	f := fixture()
	f.Nodes[1].Replies = []model.Reply{{ID: "cycle", Text: "Back", NextID: "a"}}
	if f.Validate() == nil {
		t.Fatal("cycle accepted")
	}
	f = fixture()
	s := Empty()
	s.Choices["a"] = "not-real"
	if _, e := Step(f, s, Action{Type: "tick"}, 1); e == nil {
		t.Fatal("invalid state accepted")
	}
}
func TestIndependentStates(t *testing.T) {
	f := fixture()
	first := Empty()
	second := Empty()
	first, _ = Step(f, first, Action{Type: "event", Event: "start"}, 100)
	if len(second.Events) != 0 || len(first.Events) != 1 {
		t.Fatal("state leakage")
	}
}
