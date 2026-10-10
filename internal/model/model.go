// Package model defines the versioned, language-independent authoring contracts.
package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const FormatVersion = 1
const MaxSourceBytes = 2 << 20
const MaxAssetBytes = 10 << 20
const MaxPackageBytes = 40 << 20
const MaxAssets = 100

var ErrConflict = errors.New("revision_conflict")
var ErrNotFound = errors.New("not_found")
var ErrBusy = errors.New("project_busy")
var ErrBudget = errors.New("generation_budget_exhausted")
var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)

func ValidID(s string) bool { return identifier.MatchString(s) }
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func Now() time.Time { return time.Now().UTC() }

type Source struct {
	HTML       string `json:"html"`
	CSS        string `json:"css"`
	JavaScript string `json:"javascript"`
}
type Character struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Identity string `json:"identity,omitempty"`
}
type Effect struct {
	Type   string `json:"type"`
	Target string `json:"target"`
}
type Reply struct {
	ID       string   `json:"id"`
	Text     string   `json:"text"`
	NextID   string   `json:"nextId,omitempty"`
	Requires []string `json:"requires,omitempty"`
	Effects  []Effect `json:"effects,omitempty"`
}
type StoryNode struct {
	ID          string   `json:"id"`
	CharacterID string   `json:"characterId"`
	Text        string   `json:"text"`
	Event       string   `json:"event,omitempty"`
	Requires    []string `json:"requires,omitempty"`
	DelayMS     int64    `json:"delayMs"`
	Replies     []Reply  `json:"replies"`
}
type Story struct {
	Version    int         `json:"version"`
	Characters []Character `json:"characters"`
	Nodes      []StoryNode `json:"nodes"`
}

func EmptyStory() Story { return Story{Version: 1, Characters: []Character{}, Nodes: []StoryNode{}} }

type Content struct {
	Source Source `json:"source"`
	Story  Story  `json:"story"`
}
type Asset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	MIME string `json:"mime"`
	Data []byte `json:"data"`
}
type Version struct {
	ID        string    `json:"id"`
	Number    int       `json:"number"`
	CreatedAt time.Time `json:"createdAt"`
	Summary   string    `json:"summary"`
	TaskID    string    `json:"taskId,omitempty"`
	Content   Content   `json:"content"`
}
type Project struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Revision  string    `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Content   Content   `json:"content"`
	Assets    []Asset   `json:"assets"`
	Undo      []Content `json:"undo"`
	Redo      []Content `json:"redo"`
	Versions  []Version `json:"versions"`
}
type ProjectSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Revision  string    `json:"revision"`
	UpdatedAt time.Time `json:"updatedAt"`
	Versions  int       `json:"versions"`
	Assets    int       `json:"assets"`
}
type Package struct {
	Format  string  `json:"format"`
	Version int     `json:"version"`
	Title   string  `json:"title"`
	Content Content `json:"content"`
	Assets  []Asset `json:"assets"`
}
type Usage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	Calls        int   `json:"calls"`
}
type Task struct {
	ID                  string    `json:"id"`
	ProjectID           string    `json:"projectId"`
	RequestID           string    `json:"requestId"`
	Prompt              string    `json:"prompt"`
	BaseRevision        string    `json:"baseRevision"`
	BaseContent         Content   `json:"baseContent"`
	State               string    `json:"state"`
	Phase               string    `json:"phase"`
	Error               string    `json:"error,omitempty"`
	Output              string    `json:"output,omitempty"`
	Checkpoint          *Content  `json:"checkpoint,omitempty"`
	Usage               Usage     `json:"usage"`
	Attempts            int       `json:"attempts"`
	Resumes             int       `json:"resumes"`
	ProviderFingerprint string    `json:"providerFingerprint"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

func Active(state string) bool { return state == "queued" || state == "running" }

func (s Source) Validate() error {
	if strings.TrimSpace(s.HTML) == "" {
		return errors.New("html_required")
	}
	if len(s.HTML)+len(s.CSS)+len(s.JavaScript) > MaxSourceBytes {
		return errors.New("source_too_large")
	}
	return nil
}
func (c Content) Validate() error {
	if err := c.Source.Validate(); err != nil {
		return err
	}
	return c.Story.Validate()
}
func (s Story) Validate() error {
	if s.Version != 1 {
		return errors.New("unsupported_story_version")
	}
	if len(s.Characters) > 30 || len(s.Nodes) > 300 {
		return errors.New("story_too_large")
	}
	actors, nodes, ids := map[string]bool{}, map[string]StoryNode{}, map[string]bool{}
	add := func(id string) error {
		if !ValidID(id) || ids[id] {
			return fmt.Errorf("invalid_or_duplicate_story_id: %s", id)
		}
		ids[id] = true
		return nil
	}
	for _, a := range s.Characters {
		if err := add(a.ID); err != nil {
			return err
		}
		if strings.TrimSpace(a.Name) == "" || len(a.Name) > 240 || len(a.Identity) > 1000 {
			return errors.New("invalid_character")
		}
		actors[a.ID] = true
	}
	for _, n := range s.Nodes {
		if err := add(n.ID); err != nil {
			return err
		}
		nodes[n.ID] = n
	}
	for _, n := range s.Nodes {
		if !actors[n.CharacterID] || strings.TrimSpace(n.Text) == "" || len(n.Text) > 16000 || n.DelayMS < 0 || n.DelayMS > 86400000 || len(n.Replies) > 12 || len(n.Requires) > 32 {
			return fmt.Errorf("invalid_story_node: %s", n.ID)
		}
		for _, event := range append(append([]string{}, n.Requires...), n.Event) {
			if event != "" && len(event) > 160 {
				return errors.New("invalid_event")
			}
		}
		for _, r := range n.Replies {
			if err := add(r.ID); err != nil {
				return err
			}
			if strings.TrimSpace(r.Text) == "" || len(r.Text) > 1200 || len(r.Requires) > 32 || len(r.Effects) > 32 {
				return errors.New("invalid_reply")
			}
			if r.NextID != "" {
				if _, ok := nodes[r.NextID]; !ok {
					return errors.New("unknown_next_node")
				}
			}
			for _, e := range r.Effects {
				if e.Type != "unlock" || !ValidID(e.Target) {
					return errors.New("invalid_effect")
				}
			}
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return errors.New("story_reply_cycle")
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, r := range nodes[id].Replies {
			if r.NextID != "" {
				if err := visit(r.NextID); err != nil {
					return err
				}
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	for id := range nodes {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
func AllowedMIME(m string) bool {
	switch m {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "audio/mpeg", "audio/wav", "audio/ogg", "video/mp4", "video/webm":
		return true
	}
	return false
}
func (a Asset) Validate() error {
	if !ValidID(a.ID) || a.Name == "" || len(a.Name) > 240 || !AllowedMIME(a.MIME) || len(a.Data) == 0 || len(a.Data) > MaxAssetBytes {
		return errors.New("invalid_asset")
	}
	return nil
}
func (p Package) Validate() error {
	if p.Format != "playfielder" || p.Version != FormatVersion {
		return errors.New("unsupported_package_format")
	}
	if strings.TrimSpace(p.Title) == "" || len(p.Title) > 240 || len(p.Assets) > MaxAssets {
		return errors.New("invalid_package")
	}
	if err := p.Content.Validate(); err != nil {
		return err
	}
	ids, total := map[string]bool{}, 0
	for _, a := range p.Assets {
		if err := a.Validate(); err != nil {
			return err
		}
		if ids[a.ID] {
			return errors.New("duplicate_asset")
		}
		ids[a.ID] = true
		total += len(a.Data)
	}
	if total > MaxPackageBytes {
		return errors.New("package_too_large")
	}
	return nil
}
func Equal(a, b Content) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
