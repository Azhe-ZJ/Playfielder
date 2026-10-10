// Package document prepares authored HTML using mature parsers and constructs
// isolated browser documents. Authored JavaScript is parsed, never executed here.
package document

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/Azhe-ZJ/Playfielder/internal/legal"
	"github.com/Azhe-ZJ/Playfielder/internal/model"
	"github.com/dop251/goja/parser"
	"golang.org/x/net/html"
)

//go:embed runtime.js wasm_exec.js story.wasm
var files embed.FS

var assetRef = regexp.MustCompile(`asset://([a-zA-Z0-9][a-zA-Z0-9_-]{0,79})`)

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func setAttr(n *html.Node, key, value string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: value})
}
func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}
func body(tree *html.Node) *html.Node {
	var out *html.Node
	walk(tree, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" {
			out = n
		}
	})
	return out
}
func inner(n *html.Node) string {
	var b bytes.Buffer
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&b, c)
	}
	return b.String()
}
func editable(n *html.Node) bool {
	switch n.Data {
	case "h1", "h2", "h3", "h4", "p", "span", "strong", "b", "small", "a", "button", "summary", "figcaption", "td", "th", "label", "img", "option":
		return true
	}
	return attr(n, "data-page") != ""
}
func Prepare(s model.Source) (model.Source, error) {
	if err := s.Validate(); err != nil {
		return s, err
	}
	tree, err := html.Parse(strings.NewReader(s.HTML))
	if err != nil {
		return s, err
	}
	b := body(tree)
	if b == nil {
		return s, errors.New("html_body_missing")
	}
	seen := map[string]bool{}
	walk(b, func(n *html.Node) {
		if n.Type != html.ElementNode || !editable(n) {
			return
		}
		id := attr(n, "data-wb-node")
		if !model.ValidID(id) || seen[id] {
			id = "n_" + model.NewID()
			setAttr(n, "data-wb-node", id)
		}
		seen[id] = true
	})
	s.HTML = inner(b)
	if _, err := parser.ParseFile(nil, "game.js", s.JavaScript, 0); err != nil {
		return s, errors.New("javascript_syntax_invalid")
	}
	return s, nil
}

type Edit struct {
	NodeID    string `json:"nodeId"`
	Operation string `json:"operation"`
	Value     string `json:"value"`
}

func EditNode(s model.Source, e Edit) (model.Source, error) {
	if !model.ValidID(e.NodeID) || len(e.Value) > 24000 {
		return s, errors.New("invalid_node_edit")
	}
	tree, err := html.Parse(strings.NewReader(s.HTML))
	if err != nil {
		return s, err
	}
	var target *html.Node
	walk(tree, func(n *html.Node) {
		if n.Type == html.ElementNode && attr(n, "data-wb-node") == e.NodeID {
			target = n
		}
	})
	if target == nil {
		return s, model.ErrNotFound
	}
	switch e.Operation {
	case "text":
		if target.Data == "img" {
			return s, errors.New("image_requires_source")
		}
		for target.FirstChild != nil {
			target.RemoveChild(target.FirstChild)
		}
		target.AppendChild(&html.Node{Type: html.TextNode, Data: e.Value})
	case "image":
		if target.Data != "img" || !strings.HasPrefix(e.Value, "asset://") {
			return s, errors.New("invalid_image_reference")
		}
		setAttr(target, "src", e.Value)
	default:
		return s, errors.New("unsupported_edit")
	}
	s.HTML = inner(body(tree))
	return s, nil
}
func ImportHTML(raw string) (model.Source, error) {
	if len(raw) > model.MaxSourceBytes {
		return model.Source{}, errors.New("html_too_large")
	}
	tree, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return model.Source{}, err
	}
	s := model.Source{}
	remove := []*html.Node{}
	walk(tree, func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "script" {
				if attr(n, "src") == "" {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						s.JavaScript += c.Data + "\n"
					}
				}
				remove = append(remove, n)
			}
			if n.Data == "style" {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					s.CSS += c.Data + "\n"
				}
				remove = append(remove, n)
			}
		}
	})
	for _, n := range remove {
		n.Parent.RemoveChild(n)
	}
	s.HTML = inner(body(tree))
	return Prepare(s)
}

type Options struct {
	Title       string
	Content     model.Content
	Assets      []model.Asset
	Edit        bool
	Token       string
	SaveKey     string
	ScriptNonce string
}

func resolve(raw string, assets []model.Asset) (string, error) {
	byID := map[string]model.Asset{}
	for _, a := range assets {
		if e := a.Validate(); e != nil {
			return "", e
		}
		byID[a.ID] = a
	}
	var missing string
	out := assetRef.ReplaceAllStringFunc(raw, func(ref string) string {
		id := strings.TrimPrefix(ref, "asset://")
		a, ok := byID[id]
		if !ok {
			missing = id
			return ref
		}
		return "data:" + a.MIME + ";base64," + base64.StdEncoding.EncodeToString(a.Data)
	})
	if missing != "" {
		return "", fmt.Errorf("missing_asset: %s", missing)
	}
	return out, nil
}
func nonce() string {
	var b [18]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func Build(o Options) (string, error) {
	if err := o.Content.Validate(); err != nil {
		return "", err
	}
	s := o.Content.Source
	var err error
	s.HTML, err = resolve(s.HTML, o.Assets)
	if err != nil {
		return "", err
	}
	s.CSS, err = resolve(s.CSS, o.Assets)
	if err != nil {
		return "", err
	}
	s.JavaScript, err = resolve(s.JavaScript, o.Assets)
	if err != nil {
		return "", err
	}
	n := nonce()
	if o.ScriptNonce != "" {
		n = o.ScriptNonce
	}
	tree, err := html.Parse(strings.NewReader(s.HTML))
	if err != nil {
		return "", err
	}
	remove := []*html.Node{}
	walk(tree, func(el *html.Node) {
		if el.Type != html.ElementNode {
			return
		}
		switch el.Data {
		case "base", "meta", "iframe", "object", "embed", "link":
			remove = append(remove, el)
		case "script":
			setAttr(el, "nonce", n)
			el.Attr = filterAttrs(el.Attr, func(a html.Attribute) bool { return a.Key != "src" })
		}
		el.Attr = filterAttrs(el.Attr, func(a html.Attribute) bool { return !strings.HasPrefix(a.Key, "on") && a.Key != "srcdoc" })
	})
	for _, el := range remove {
		if el.Parent != nil {
			el.Parent.RemoveChild(el)
		}
	}
	s.HTML = inner(body(tree))
	config, _ := json.Marshal(struct {
		Flow    model.Story `json:"flow"`
		Edit    bool        `json:"edit"`
		Token   string      `json:"token"`
		SaveKey string      `json:"saveKey"`
	}{o.Content.Story, o.Edit, o.Token, o.SaveKey})
	runtime, _ := files.ReadFile("runtime.js")
	var wasmSupport string
	if len(o.Content.Story.Nodes) > 0 {
		wasm, _ := files.ReadFile("story.wasm")
		exec, _ := files.ReadFile("wasm_exec.js")
		wasmSupport = string(exec) + "\nwindow.__pfWasm='" + base64.StdEncoding.EncodeToString(wasm) + "';\n"
	}
	csp := "default-src 'none'; script-src 'nonce-" + n + "' 'wasm-unsafe-eval'; style-src 'unsafe-inline'; img-src data:; media-src data:; font-src data:; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"
	return "<!doctype html>" + legal.HTMLNotice() + "<html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><meta http-equiv=\"Content-Security-Policy\" content=\"" + csp + "\"><title>" + escape(o.Title) + "</title><style>" + strings.ReplaceAll(s.CSS, "</style", "<\\/style") + "</style></head><body>" + s.HTML + "<script nonce=\"" + n + "\">window.__pfConfig=" + string(config) + ";\n" + wasmSupport + string(runtime) + "</script><script nonce=\"" + n + "\">" + strings.ReplaceAll(s.JavaScript, "</script", "<\\/script") + "</script></body></html>", nil
}
func filterAttrs(xs []html.Attribute, keep func(html.Attribute) bool) []html.Attribute {
	out := []html.Attribute{}
	for _, a := range xs {
		if keep(a) {
			out = append(out, a)
		}
	}
	return out
}
func escape(s string) string {
	var b bytes.Buffer
	_ = html.Render(&b, &html.Node{Type: html.TextNode, Data: s})
	return b.String()
}
func ReadLimited(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("input_too_large")
	}
	return data, nil
}
