// Package story is a deterministic narrative state machine. Time is injected.
package story

import (
	"errors"
	"fmt"
	"sort"

	"github.com/Azhe-ZJ/Playfielder/internal/model"
)

type Pending struct {
	NodeID string `json:"nodeId"`
	DueAt  int64  `json:"dueAt"`
}
type Message struct {
	ID      string `json:"id"`
	NodeID  string `json:"nodeId"`
	ReplyID string `json:"replyId,omitempty"`
	At      int64  `json:"at"`
}
type State struct {
	Events    []string          `json:"events"`
	Activated []string          `json:"activated"`
	Fired     []string          `json:"fired"`
	Pending   []Pending         `json:"pending"`
	Messages  []Message         `json:"messages"`
	Choices   map[string]string `json:"choices"`
	Unlocked  []string          `json:"unlocked"`
	Read      []string          `json:"read"`
}
type Action struct {
	Type    string   `json:"type"`
	Event   string   `json:"event,omitempty"`
	NodeID  string   `json:"nodeId,omitempty"`
	ReplyID string   `json:"replyId,omitempty"`
	IDs     []string `json:"ids,omitempty"`
}

func Empty() State {
	return State{Events: []string{}, Activated: []string{}, Fired: []string{}, Pending: []Pending{}, Messages: []Message{}, Choices: map[string]string{}, Unlocked: []string{}, Read: []string{}}
}
func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
func add(xs []string, s string) []string {
	if !contains(xs, s) {
		return append(xs, s)
	}
	return xs
}
func met(req, events []string) bool {
	for _, r := range req {
		if !contains(events, r) {
			return false
		}
	}
	return true
}
func Available(flow model.Story, state State, id string) []model.Reply {
	if state.Choices[id] != "" {
		return []model.Reply{}
	}
	delivered := false
	for _, m := range state.Messages {
		if m.NodeID == id && m.ReplyID == "" {
			delivered = true
		}
	}
	if !delivered {
		return []model.Reply{}
	}
	for _, n := range flow.Nodes {
		if n.ID == id {
			out := []model.Reply{}
			for _, r := range n.Replies {
				if met(r.Requires, state.Events) {
					out = append(out, r)
				}
			}
			return out
		}
	}
	return []model.Reply{}
}
func ValidateState(flow model.Story, s State) error {
	if len(s.Events) > 4096 || len(s.Pending) > 300 || len(s.Messages) > 600 || len(s.Choices) > 300 || len(s.Unlocked) > 4096 {
		return errors.New("state_too_large")
	}
	nodes := map[string]model.StoryNode{}
	replies := map[string]string{}
	for _, n := range flow.Nodes {
		nodes[n.ID] = n
		for _, r := range n.Replies {
			replies[r.ID] = n.ID
		}
	}
	for _, id := range append(append([]string{}, s.Activated...), s.Fired...) {
		if _, ok := nodes[id]; !ok {
			return errors.New("unknown_state_node")
		}
	}
	for _, p := range s.Pending {
		if _, ok := nodes[p.NodeID]; !ok || p.DueAt < 0 {
			return errors.New("invalid_pending")
		}
	}
	for _, m := range s.Messages {
		if _, ok := nodes[m.NodeID]; !ok {
			return errors.New("unknown_message_node")
		}
		if m.ReplyID != "" && replies[m.ReplyID] != m.NodeID {
			return errors.New("invalid_message_reply")
		}
	}
	for id, r := range s.Choices {
		if replies[r] != id {
			return errors.New("invalid_choice")
		}
	}
	return nil
}
func Step(flow model.Story, previous State, a Action, now int64) (State, error) {
	if err := flow.Validate(); err != nil {
		return State{}, err
	}
	if err := ValidateState(flow, previous); err != nil {
		return State{}, err
	}
	s := previous
	s.Events = append([]string{}, previous.Events...)
	s.Activated = append([]string{}, previous.Activated...)
	s.Fired = append([]string{}, previous.Fired...)
	s.Pending = append([]Pending{}, previous.Pending...)
	s.Messages = append([]Message{}, previous.Messages...)
	s.Unlocked = append([]string{}, previous.Unlocked...)
	s.Read = append([]string{}, previous.Read...)
	s.Choices = map[string]string{}
	for k, v := range previous.Choices {
		s.Choices[k] = v
	}
	deliver := func() {
		sort.SliceStable(s.Pending, func(i, j int) bool { return s.Pending[i].DueAt < s.Pending[j].DueAt })
		keep := []Pending{}
		for _, p := range s.Pending {
			if p.DueAt <= now {
				s.Messages = append(s.Messages, Message{ID: p.NodeID, NodeID: p.NodeID, At: p.DueAt})
			} else {
				keep = append(keep, p)
			}
		}
		s.Pending = keep
	}
	activate := func() {
		for _, n := range flow.Nodes {
			if ((n.Event != "" && contains(s.Events, n.Event)) || contains(s.Activated, n.ID)) && met(n.Requires, s.Events) && !contains(s.Fired, n.ID) {
				s.Fired = add(s.Fired, n.ID)
				s.Pending = append(s.Pending, Pending{n.ID, now + n.DelayMS})
			}
		}
	}
	deliver()
	switch a.Type {
	case "event":
		if a.Event == "" || len(a.Event) > 160 {
			return State{}, errors.New("invalid_event")
		}
		s.Events = add(s.Events, a.Event)
	case "reply":
		activate()
		deliver()
		found := false
		for _, r := range Available(flow, s, a.NodeID) {
			if r.ID == a.ReplyID {
				found = true
				s.Choices[a.NodeID] = r.ID
				s.Messages = append(s.Messages, Message{ID: r.ID, NodeID: a.NodeID, ReplyID: r.ID, At: now})
				for _, e := range r.Effects {
					s.Unlocked = add(s.Unlocked, e.Target)
				}
				if r.NextID != "" {
					s.Activated = add(s.Activated, r.NextID)
				}
			}
		}
		if !found {
			return State{}, errors.New("reply_unavailable")
		}
	case "read":
		for _, id := range a.IDs {
			for _, m := range s.Messages {
				if m.ID == id && m.ReplyID == "" {
					s.Read = add(s.Read, id)
				}
			}
		}
	case "tick":
	default:
		return State{}, fmt.Errorf("unknown_story_action: %s", a.Type)
	}
	activate()
	deliver()
	return s, nil
}
