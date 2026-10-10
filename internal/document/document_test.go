package document

import (
	"strings"
	"testing"

	"github.com/Azhe-ZJ/Playfielder/internal/model"
	"golang.org/x/net/html"
)

func TestPrepareStableIDsAndTextEdit(t *testing.T) {
	s, e := Prepare(model.Source{HTML: `<h1>Hello</h1><p data-wb-node="same">A</p><p data-wb-node="same">B</p>`})
	if e != nil {
		t.Fatal(e)
	}
	again, e := Prepare(s)
	if e != nil || again.HTML != s.HTML {
		t.Fatal("IDs are not stable")
	}
	tree, _ := html.Parse(strings.NewReader(s.HTML))
	id := ""
	walk(tree, func(n *html.Node) {
		if n.Data == "h1" {
			id = attr(n, "data-wb-node")
		}
	})
	out, e := EditNode(s, Edit{NodeID: id, Operation: "text", Value: `<script>alert(1)</script>`})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(out.HTML, "<script>") || !strings.Contains(out.HTML, "&lt;script&gt;") {
		t.Fatal("text was interpreted as HTML")
	}
}
func TestSyntaxAndAssets(t *testing.T) {
	if _, e := Prepare(model.Source{HTML: "<p>test</p>", JavaScript: "const = ;"}); e == nil {
		t.Fatal("invalid JS accepted")
	}
	c := model.Content{Source: model.Source{HTML: `<img src="asset://missing">`}, Story: model.EmptyStory()}
	if _, e := Build(Options{Title: "x", Content: c}); e == nil {
		t.Fatal("missing asset accepted")
	}
	c.Source.HTML = "<h1>test</h1>"
	out, e := Build(Options{Title: `<img onerror="alert(1)">`, Content: c, Token: "token"})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out, "connect-src 'none'") || !strings.Contains(out, "&lt;img") {
		t.Fatal("missing document isolation")
	}
}
func TestImportCompleteHTML(t *testing.T) {
	s, e := ImportHTML(`<!doctype html><html><head><style>p{color:red}</style></head><body><p>Keep me</p><script>const n=1;</script></body></html>`)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(s.HTML, "Keep me") || !strings.Contains(s.CSS, "color:red") || !strings.Contains(s.JavaScript, "const n=1") {
		t.Fatal("import dropped content")
	}
}
func TestUnsafeEmbeddingRemoved(t *testing.T) {
	c := model.Content{Source: model.Source{HTML: `<base href="https://example.com"><iframe src="https://example.com"></iframe><button onclick="bad()">x</button>`}, Story: model.EmptyStory()}
	s, e := Build(Options{Title: "x", Content: c})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(s, "onclick=") || strings.Contains(s, "<iframe") || strings.Contains(s, "<base ") {
		t.Fatal("unsafe markup retained")
	}
}
