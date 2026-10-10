package packageio

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/Azhe-ZJ/Playfielder/internal/model"
)

func TestRoundTripPlayableAndEditable(t *testing.T) {
	p := model.Project{ID: "test", Title: "档案", Content: model.Content{Source: model.Source{HTML: `<h1>故事</h1><img src="asset://picture">`, CSS: "body{color:red}"}, Story: model.EmptyStory()}, Assets: []model.Asset{{ID: "picture", Name: "图.png", MIME: "image/png", Data: []byte{137, 80, 78, 71}}}}
	data, e := Export(p)
	if e != nil {
		t.Fatal(e)
	}
	imported, e := FromZIP(data)
	if e != nil {
		t.Fatal(e)
	}
	if imported.Title != p.Title || imported.Content.Source.CSS != p.Content.Source.CSS || !bytes.Equal(imported.Assets[0].Data, p.Assets[0].Data) {
		t.Fatal("roundtrip lost data")
	}
	r, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	found := false
	for _, f := range r.File {
		if f.Name == "index.html" {
			found = true
		}
	}
	if !found {
		t.Fatal("no standalone player")
	}
}
func archive(name string, mode os.FileMode) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: name}
	h.SetMode(mode)
	f, _ := w.CreateHeader(h)
	f.Write([]byte("bad"))
	w.Close()
	return b.Bytes()
}
func TestUnsafeArchives(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "safe/../escape", `a\b`, "C:/file"} {
		if _, e := FromZIP(archive(name, 0600)); e == nil {
			t.Errorf("accepted %s", name)
		}
	}
	if _, e := FromZIP(archive("link", os.ModeSymlink|0600)); e == nil {
		t.Fatal("symlink accepted")
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for i := 0; i < 2; i++ {
		f, _ := w.Create("manifest.json")
		f.Write([]byte(`{}`))
	}
	w.Close()
	if _, e := FromZIP(b.Bytes()); e == nil {
		t.Fatal("duplicate names accepted")
	}
}
func TestLegacyAdapterAndNoPrivateFields(t *testing.T) {
	raw := []byte(`{"title":"Legacy","activeVersion":2,"settings":{"apiKey":"must-not-import"},"artifacts":[{"version":1,"source":{"html":"<h1>Old</h1>","css":"","javascript":""}},{"version":2,"source":{"html":"<h1>New</h1>","css":"","javascript":""}}]}`)
	p, e := FromJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(p.Content.Source.HTML, "New") {
		t.Fatal("wrong active version")
	}
	if _, e = FromJSON([]byte(`{"format":"playfielder","version":99}`)); e == nil {
		t.Fatal("unknown format accepted")
	}
	if _, e = Import([]byte(`<h1>Simple</h1>`), "simple.html"); e != nil {
		t.Fatal(e)
	}
}
