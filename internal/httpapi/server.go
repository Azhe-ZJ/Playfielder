// Package httpapi exposes a single-author, loopback-only workspace. It is not a
// hosted account system. Both the listener and request origin enforce this scope.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azhe-ZJ/Playfielder/internal/agent"
	"github.com/Azhe-ZJ/Playfielder/internal/document"
	"github.com/Azhe-ZJ/Playfielder/internal/model"
	"github.com/Azhe-ZJ/Playfielder/internal/packageio"
	"github.com/Azhe-ZJ/Playfielder/internal/store"
	"github.com/Azhe-ZJ/Playfielder/internal/story"
	"github.com/Azhe-ZJ/Playfielder/web"
)

type Server struct {
	Store       *store.Store
	Engine      *agent.Engine
	token, csrf string
	mux         *http.ServeMux
}

func New(s *store.Store, e *agent.Engine) *Server {
	a := &Server{Store: s, Engine: e, token: model.NewID() + model.NewID(), csrf: model.NewID(), mux: http.NewServeMux()}
	a.mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		a.json(w, 200, map[string]interface{}{"name": "Playfielder", "version": "0.1.0", "formatVersion": 1, "csrf": a.csrf, "aiConfigured": e.Configured(), "scope": "local-single-author"})
	})
	a.mux.HandleFunc("GET /api/projects", a.list)
	a.mux.HandleFunc("POST /api/projects", a.create)
	a.mux.HandleFunc("POST /api/import", a.importProject)
	a.mux.HandleFunc("GET /api/projects/{id}", a.get)
	a.mux.HandleFunc("PUT /api/projects/{id}", a.save)
	a.mux.HandleFunc("POST /api/projects/{id}/edit", a.edit)
	a.mux.HandleFunc("POST /api/projects/{id}/undo", a.undo)
	a.mux.HandleFunc("POST /api/projects/{id}/snapshot", a.snapshot)
	a.mux.HandleFunc("POST /api/projects/{id}/restore", a.restore)
	a.mux.HandleFunc("POST /api/projects/{id}/assets", a.asset)
	a.mux.HandleFunc("POST /api/projects/{id}/preview", a.preview)
	a.mux.HandleFunc("GET /api/projects/{id}/export", a.export)
	a.mux.HandleFunc("GET /api/projects/{id}/export.html", a.exportHTML)
	a.mux.HandleFunc("POST /api/projects/{id}/publish", a.publish)
	a.mux.HandleFunc("GET /play/{id}", a.play)
	a.mux.HandleFunc("POST /api/projects/{id}/story", a.storyStep)
	a.mux.HandleFunc("POST /api/projects/{id}/story/reset", a.storyReset)
	a.mux.HandleFunc("GET /api/projects/{id}/tasks", a.tasks)
	a.mux.HandleFunc("POST /api/projects/{id}/tasks", a.start)
	a.mux.HandleFunc("GET /api/tasks/{id}", a.task)
	a.mux.HandleFunc("GET /api/tasks/{id}/events", a.events)
	a.mux.HandleFunc("POST /api/tasks/{id}/cancel", a.cancel)
	a.mux.HandleFunc("POST /api/tasks/{id}/resume", a.resume)
	a.mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { a.json(w, 200, map[string]string{"status": "ok"}) })
	assets, _ := fs.Sub(web.Files, "assets")
	static := http.FileServer(http.FS(assets))
	a.mux.Handle("GET /static/", http.StripPrefix("/static/", static))
	a.mux.HandleFunc("GET /", a.index)
	return a
}
func AllowedAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func localHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
func (a *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-"+a.csrf+"' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src data:; frame-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	if !localHost(r.Host) {
		a.error(w, errors.New("loopback_host_required"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		cookie, err := r.Cookie("pf_local_session")
		if err != nil || cookie.Value != a.token {
			a.json(w, 401, map[string]string{"error": "local_session_required"})
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if r.Header.Get("Origin") != "http://"+r.Host || r.Header.Get("X-Playfielder-CSRF") != a.csrf {
				a.json(w, 403, map[string]string{"error": "origin_or_csrf_rejected"})
				return
			}
		}
	}
	a.mux.ServeHTTP(w, r)
}
func (a *Server) json(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (a *Server) error(w http.ResponseWriter, e error) {
	status := 400
	message := e.Error()
	switch {
	case errors.Is(e, model.ErrNotFound):
		status = 404
	case errors.Is(e, model.ErrConflict), errors.Is(e, model.ErrBusy):
		status = 409
	case errors.Is(e, model.ErrBudget):
		status = 429
	}
	if len(message) > 160 {
		message = "request_failed"
	}
	if strings.Contains(message, "playfielder.db") || strings.Contains(message, "bbolt") {
		status = 500
		message = "storage_error"
	}
	a.json(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, v interface{}) error {
	if media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || media != "application/json" {
		return errors.New("json_content_type_required")
	}
	r.Body = http.MaxBytesReader(w, r.Body, model.MaxSourceBytes+(1<<20))
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("invalid_request_json")
	}
	var trailing interface{}
	if e := d.Decode(&trailing); !errors.Is(e, io.EOF) {
		return errors.New("trailing_request_data")
	}
	return nil
}
func publicProject(p model.Project) model.Project {
	p.Assets = append([]model.Asset{}, p.Assets...)
	for i := range p.Assets {
		p.Assets[i].Data = nil
	}
	return p
}
func (a *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "pf_local_session", Value: a.token, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: 86400})
	b, _ := web.Files.ReadFile("assets/index.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}
func (a *Server) list(w http.ResponseWriter, r *http.Request) {
	out, e := a.Store.List()
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, out)
}
func (a *Server) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title    string `json:"title"`
		Template string `json:"template"`
	}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	if in.Title == "" {
		in.Title = "未命名调查"
	}
	c := Template(in.Template)
	s, e := document.Prepare(c.Source)
	if e != nil {
		a.error(w, e)
		return
	}
	c.Source = s
	p, e := a.Store.Create(model.Package{Format: "playfielder", Version: 1, Title: in.Title, Content: c, Assets: []model.Asset{}})
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 201, publicProject(p))
}
func (a *Server) get(w http.ResponseWriter, r *http.Request) {
	p, e := a.Store.Get(r.PathValue("id"))
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, publicProject(p))
}
func (a *Server) save(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision string        `json:"revision"`
		Title    string        `json:"title"`
		Content  model.Content `json:"content"`
	}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	s, e := document.Prepare(in.Content.Source)
	if e != nil {
		a.error(w, e)
		return
	}
	in.Content.Source = s
	p, e := a.Store.Save(r.PathValue("id"), in.Revision, in.Title, in.Content)
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, publicProject(p))
}
func (a *Server) edit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision string        `json:"revision"`
		Edit     document.Edit `json:"edit"`
	}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	p, e := a.Store.Get(r.PathValue("id"))
	if e != nil {
		a.error(w, e)
		return
	}
	p.Content.Source, e = document.EditNode(p.Content.Source, in.Edit)
	if e != nil {
		a.error(w, e)
		return
	}
	p, e = a.Store.Save(p.ID, in.Revision, p.Title, p.Content)
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, publicProject(p))
}
func (a *Server) undo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision string `json:"revision"`
		Redo     bool   `json:"redo"`
	}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	p, e := a.Store.Undo(r.PathValue("id"), in.Revision, in.Redo)
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, publicProject(p))
}
func (a *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision string `json:"revision"`
		Summary  string `json:"summary"`
	}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	p, e := a.Store.Snapshot(r.PathValue("id"), in.Revision, in.Summary)
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, publicProject(p))
}
func (a *Server) restore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision  string `json:"revision"`
		VersionID string `json:"versionId"`
	}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	p, e := a.Store.Restore(r.PathValue("id"), in.Revision, in.VersionID)
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, publicProject(p))
}
func multipartFile(w http.ResponseWriter, r *http.Request, max int64) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, max+(1<<20))
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		return nil, "", errors.New("upload_too_large_or_invalid")
	}
	defer r.MultipartForm.RemoveAll()
	file, h, e := r.FormFile("file")
	if e != nil {
		return nil, "", errors.New("upload_file_required")
	}
	defer file.Close()
	b, e := document.ReadLimited(file, max)
	return b, filepath.Base(h.Filename), e
}
func (a *Server) asset(w http.ResponseWriter, r *http.Request) {
	b, name, e := multipartFile(w, r, model.MaxAssetBytes)
	if e != nil {
		a.error(w, e)
		return
	}
	media := strings.Split(http.DetectContentType(b), ";")[0]
	asset := model.Asset{ID: model.NewID(), Name: name, MIME: media, Data: b}
	p, e := a.Store.AddAsset(r.PathValue("id"), r.FormValue("revision"), asset)
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 201, map[string]interface{}{"project": publicProject(p), "assetId": asset.ID})
}
func (a *Server) importProject(w http.ResponseWriter, r *http.Request) {
	b, name, e := multipartFile(w, r, model.MaxPackageBytes)
	if e != nil {
		a.error(w, e)
		return
	}
	pkg, e := packageio.Import(b, name)
	if e != nil {
		a.error(w, e)
		return
	}
	p, e := a.Store.Create(pkg)
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 201, publicProject(p))
}
func (a *Server) preview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Edit bool `json:"edit"`
	}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	p, e := a.Store.Get(r.PathValue("id"))
	if e != nil {
		a.error(w, e)
		return
	}
	token := model.NewID()
	page, e := document.Build(document.Options{Title: p.Title, Content: p.Content, Assets: p.Assets, Edit: in.Edit, Token: token, SaveKey: p.ID, ScriptNonce: a.csrf})
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, map[string]string{"html": page, "token": token})
}
func (a *Server) export(w http.ResponseWriter, r *http.Request) {
	p, e := a.Store.Get(r.PathValue("id"))
	if e != nil {
		a.error(w, e)
		return
	}
	data, e := packageio.Export(p)
	if e != nil {
		a.error(w, e)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="playfielder-`+p.ID+`.zip"`)
	_, _ = w.Write(data)
}
func (a *Server) exportHTML(w http.ResponseWriter, r *http.Request) {
	p, e := a.Store.Get(r.PathValue("id"))
	if e != nil {
		a.error(w, e)
		return
	}
	page, e := document.Build(document.Options{Title: p.Title, Content: p.Content, Assets: p.Assets, SaveKey: p.ID, ScriptNonce: a.csrf})
	if e != nil {
		a.error(w, e)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="playfielder-`+p.ID+`.html"`)
	_, _ = io.WriteString(w, page)
}
func (a *Server) publish(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision string `json:"revision"`
	}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	p, e := a.Store.Publish(r.PathValue("id"), in.Revision)
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 201, map[string]string{"url": "/play/" + p.ID, "scope": "local"})
}
func (a *Server) play(w http.ResponseWriter, r *http.Request) {
	p, e := a.Store.Publication(r.PathValue("id"))
	if e != nil {
		a.error(w, e)
		return
	}
	page, e := document.Build(document.Options{Title: p.Title, Content: p.Content, Assets: p.Assets, SaveKey: p.ID, ScriptNonce: a.csrf})
	if e != nil {
		a.error(w, e)
		return
	}
	outer := `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Playfielder</title><link rel="stylesheet" href="/static/player.css"></head><body><iframe title="作品试玩" sandbox="allow-scripts" referrerpolicy="no-referrer" srcdoc="` + escapeAttr(page) + `"></iframe></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, outer)
}
func escapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "\"", "&quot;", "<", "&lt;", ">", "&gt;").Replace(s)
}
func (a *Server) storyStep(w http.ResponseWriter, r *http.Request) {
	var action story.Action
	if e := decode(w, r, &action); e != nil {
		a.error(w, e)
		return
	}
	state, e := a.Store.ApplyStory(r.PathValue("id"), action, time.Now().UnixMilli())
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, state)
}
func (a *Server) storyReset(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	if e := a.Store.ResetStory(r.PathValue("id")); e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, map[string]bool{"ok": true})
}
func (a *Server) tasks(w http.ResponseWriter, r *http.Request) {
	out, e := a.Store.Tasks(r.PathValue("id"))
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, out)
}
func (a *Server) start(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision  string `json:"revision"`
		RequestID string `json:"requestId"`
		Prompt    string `json:"prompt"`
	}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	t, e := a.Engine.Start(r.PathValue("id"), in.Revision, in.RequestID, in.Prompt)
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 202, t)
}
func (a *Server) task(w http.ResponseWriter, r *http.Request) {
	t, e := a.Store.Task(r.PathValue("id"))
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, t)
}
func (a *Server) cancel(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	t, e := a.Engine.Cancel(r.PathValue("id"))
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 200, t)
}
func (a *Server) resume(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if e := decode(w, r, &in); e != nil {
		a.error(w, e)
		return
	}
	t, e := a.Engine.Resume(r.PathValue("id"))
	if e != nil {
		a.error(w, e)
		return
	}
	a.json(w, 202, t)
}
func (a *Server) events(w http.ResponseWriter, r *http.Request) {
	if _, e := a.Store.Task(r.PathValue("id")); e != nil {
		a.error(w, e)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.error(w, errors.New("streaming_unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	for {
		t, e := a.Store.Task(r.PathValue("id"))
		if e != nil {
			return
		}
		data, _ := json.Marshal(t)
		if string(data) != last {
			if _, e = fmt.Fprintf(w, "event: task\ndata: %s\n\n", data); e != nil {
				return
			}
			flusher.Flush()
			last = string(data)
		}
		if !model.Active(t.State) {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
