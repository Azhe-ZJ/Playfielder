package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Azhe-ZJ/Playfielder/internal/agent"
	"github.com/Azhe-ZJ/Playfielder/internal/model"
	"github.com/Azhe-ZJ/Playfielder/internal/store"
)

func testApp(t *testing.T) (*httptest.Server, *http.Cookie, string) {
	t.Helper()
	s, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	engine, e := agent.New(s, &agent.Remote{})
	if e != nil {
		t.Fatal(e)
	}
	app := New(s, engine)
	server := httptest.NewServer(app)
	t.Cleanup(func() { server.Close(); engine.Close(); s.Close() })
	response, e := http.Get(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	return server, response.Cookies()[0], app.csrf
}
func request(t *testing.T, server *httptest.Server, cookie *http.Cookie, csrf, method, path string, body interface{}) (int, []byte) {
	t.Helper()
	var data io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		data = bytes.NewReader(b)
	}
	r, _ := http.NewRequest(method, server.URL+path, data)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	r.Header.Set("Origin", server.URL)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Playfielder-CSRF", csrf)
	response, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	out, _ := io.ReadAll(response.Body)
	return response.StatusCode, out
}
func TestLocalBoundaryAndCSRF(t *testing.T) {
	server, cookie, csrf := testApp(t)
	status, _ := request(t, server, nil, csrf, "GET", "/api/projects", nil)
	if status != 401 {
		t.Fatal("missing session accepted")
	}
	status, _ = request(t, server, cookie, "wrong", "POST", "/api/projects", map[string]string{"title": "Test"})
	if status != 403 {
		t.Fatal("missing csrf accepted")
	}
	req, _ := http.NewRequest("GET", server.URL+"/api/projects", nil)
	req.Host = "evil.example"
	req.AddCookie(cookie)
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode == 200 {
		t.Fatal("DNS rebinding host accepted")
	}
	for _, address := range []string{"0.0.0.0:4177", ":4177", "192.168.1.2:4177", "localhost:4177"} {
		if AllowedAddress(address) {
			t.Fatal("nonliteral loopback listen", address)
		}
	}
	if !AllowedAddress("127.0.0.1:4177") || !AllowedAddress("[::1]:4177") {
		t.Fatal("loopback rejected")
	}
}
func TestEditorPreviewSnapshotAndExport(t *testing.T) {
	server, cookie, csrf := testApp(t)
	status, data := request(t, server, cookie, csrf, "POST", "/api/projects", map[string]string{"title": "Browser test", "template": "blank"})
	if status != 201 {
		t.Fatal(status, string(data))
	}
	var project model.Project
	json.Unmarshal(data, &project)
	original := project.Content
	project.Content.Source.HTML = "<h1>Changed</h1>"
	status, data = request(t, server, cookie, csrf, "PUT", "/api/projects/"+project.ID, map[string]interface{}{"title": project.Title, "revision": project.Revision, "content": project.Content})
	if status != 200 {
		t.Fatal(status, string(data))
	}
	json.Unmarshal(data, &project)
	status, _ = request(t, server, cookie, csrf, "PUT", "/api/projects/"+project.ID, map[string]interface{}{"title": project.Title, "revision": "stale", "content": original})
	if status != 409 {
		t.Fatal("stale save accepted")
	}
	status, data = request(t, server, cookie, csrf, "POST", "/api/projects/"+project.ID+"/preview", map[string]bool{"edit": true})
	if status != 200 || !strings.Contains(string(data), "connect-src 'none'") {
		t.Fatal("preview failed", status)
	}
	status, data = request(t, server, cookie, csrf, "GET", "/api/projects/"+project.ID+"/export", nil)
	if status != 200 || !bytes.HasPrefix(data, []byte("PK")) {
		t.Fatal("export failed")
	}
	status, data = request(t, server, cookie, csrf, "POST", "/api/projects/"+project.ID+"/publish", map[string]string{"revision": project.Revision})
	if status != 201 {
		t.Fatal(status, string(data))
	}
	var publication map[string]string
	json.Unmarshal(data, &publication)
	response, e := http.Get(server.URL + publication["url"])
	if e != nil {
		t.Fatal(e)
	}
	outer, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(outer), `sandbox="allow-scripts"`) || strings.Contains(string(outer), "allow-same-origin") {
		t.Fatal("player is not opaque sandbox")
	}
}
func TestImportCreatesIndependentProject(t *testing.T) {
	server, cookie, csrf := testApp(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	f, _ := writer.CreateFormFile("file", "simple.html")
	f.Write([]byte(`<h1>Imported</h1>`))
	writer.Close()
	r, _ := http.NewRequest("POST", server.URL+"/api/import", &body)
	r.AddCookie(cookie)
	r.Header.Set("Origin", server.URL)
	r.Header.Set("X-Playfielder-CSRF", csrf)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	response, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != 201 || !strings.Contains(string(data), "Imported") {
		t.Fatal(response.StatusCode, string(data))
	}
	u, _ := url.Parse(server.URL)
	r, _ = http.NewRequest("POST", server.URL+"/api/import", strings.NewReader("bad"))
	r.Host = u.Host
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://foreign.example")
	r.Header.Set("X-Playfielder-CSRF", csrf)
	res, _ := http.DefaultClient.Do(r)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("foreign origin accepted")
	}
}
