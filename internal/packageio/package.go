// Package packageio reads and writes bounded, portable authoring packages.
// Archive entries are read in memory; no archive path is ever extracted to disk.
package packageio

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/Azhe-ZJ/Playfielder/internal/document"
	"github.com/Azhe-ZJ/Playfielder/internal/legal"
	"github.com/Azhe-ZJ/Playfielder/internal/model"
)

func prepare(p model.Package) (model.Package, error) {
	if err := p.Validate(); err != nil {
		return p, err
	}
	s, e := document.Prepare(p.Content.Source)
	if e != nil {
		return p, e
	}
	p.Content.Source = s
	if _, e = document.Build(document.Options{Title: p.Title, Content: p.Content, Assets: p.Assets}); e != nil {
		return p, e
	}
	return p, nil
}
func FromJSON(data []byte) (model.Package, error) {
	var native model.Package
	if err := json.Unmarshal(data, &native); err != nil {
		return native, errors.New("invalid_package_json")
	}
	if native.Format != "" {
		return prepare(native)
	}
	// Explicit adapter for the TypeScript author's document shape, not its private
	// database, accounts, settings or model configuration.
	var old struct {
		Title         string       `json:"title"`
		Source        model.Source `json:"source"`
		Story         *model.Story `json:"story"`
		ActiveVersion int          `json:"activeVersion"`
		Artifacts     []struct {
			Version int          `json:"version"`
			Source  model.Source `json:"source"`
			Story   *model.Story `json:"story"`
		} `json:"artifacts"`
		Studio *struct {
			Source model.Source `json:"source"`
			Story  *model.Story `json:"story"`
		} `json:"studio"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return native, errors.New("unsupported_json_shape")
	}
	c := model.Content{Source: old.Source, Story: model.EmptyStory()}
	if old.Story != nil {
		c.Story = *old.Story
	}
	if old.Studio != nil && old.Studio.Source.HTML != "" {
		c.Source = old.Studio.Source
		if old.Studio.Story != nil {
			c.Story = *old.Studio.Story
		}
	} else {
		for _, a := range old.Artifacts {
			if a.Version == old.ActiveVersion || (old.ActiveVersion == 0 && c.Source.HTML == "") {
				c.Source = a.Source
				if a.Story != nil {
					c.Story = *a.Story
				}
			}
		}
	}
	if old.Title == "" {
		old.Title = "Imported story"
	}
	return prepare(model.Package{Format: "playfielder", Version: 1, Title: old.Title, Content: c, Assets: []model.Asset{}})
}
func Import(data []byte, name string) (model.Package, error) {
	if len(data) > model.MaxPackageBytes {
		return model.Package{}, errors.New("package_too_large")
	}
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return FromZIP(data)
	}
	if strings.HasSuffix(strings.ToLower(name), ".html") || bytes.HasPrefix(bytes.TrimSpace(data), []byte("<")) {
		s, e := document.ImportHTML(string(data))
		if e != nil {
			return model.Package{}, e
		}
		return prepare(model.Package{Format: "playfielder", Version: 1, Title: strings.TrimSuffix(path.Base(name), path.Ext(name)), Content: model.Content{Source: s, Story: model.EmptyStory()}, Assets: []model.Asset{}})
	}
	return FromJSON(data)
}
func FromZIP(data []byte) (model.Package, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return model.Package{}, errors.New("invalid_zip")
	}
	if len(r.File) > model.MaxAssets+10 {
		return model.Package{}, errors.New("too_many_archive_entries")
	}
	entries := map[string][]byte{}
	total := int64(0)
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return model.Package{}, errors.New("non_regular_archive_entry")
		}
		if f.Name == "" || path.Clean(f.Name) != f.Name || strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, "\\") || strings.Contains(f.Name, ":") || strings.HasPrefix(f.Name, "../") {
			return model.Package{}, errors.New("unsafe_archive_path")
		}
		if _, ok := entries[f.Name]; ok {
			return model.Package{}, errors.New("duplicate_archive_entry")
		}
		if f.UncompressedSize64 > uint64(model.MaxPackageBytes) {
			return model.Package{}, errors.New("archive_entry_too_large")
		}
		reader, e := f.Open()
		if e != nil {
			return model.Package{}, e
		}
		b, e := document.ReadLimited(reader, int64(model.MaxPackageBytes)-total)
		reader.Close()
		if e != nil {
			return model.Package{}, e
		}
		total += int64(len(b))
		entries[f.Name] = b
	}
	manifest, ok := entries["manifest.json"]
	if !ok {
		return model.Package{}, errors.New("manifest_required_use_html_import_for_other_games")
	}
	var pkg model.Package
	if e := json.Unmarshal(manifest, &pkg); e != nil {
		return pkg, errors.New("invalid_manifest")
	}
	for i, a := range pkg.Assets {
		if !model.ValidID(a.ID) {
			return pkg, errors.New("invalid_asset_id")
		}
		b, ok := entries["assets/"+a.ID]
		if !ok {
			return pkg, errors.New("asset_missing_from_archive")
		}
		pkg.Assets[i].Data = b
	}
	return prepare(pkg)
}
func Export(p model.Project) ([]byte, error) {
	pkg := model.Package{Format: "playfielder", Version: 1, Title: p.Title, Content: p.Content, Assets: p.Assets}
	if err := pkg.Validate(); err != nil {
		return nil, err
	}
	page, err := document.Build(document.Options{Title: p.Title, Content: p.Content, Assets: p.Assets, SaveKey: p.ID})
	if err != nil {
		return nil, err
	}
	metadata := pkg
	metadata.Assets = append([]model.Asset{}, pkg.Assets...)
	for i := range metadata.Assets {
		metadata.Assets[i].Data = nil
	}
	manifest, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	write := func(name string, data []byte) error {
		f, e := w.Create(name)
		if e != nil {
			return e
		}
		_, e = f.Write(data)
		return e
	}
	for _, name := range []string{"LICENSE", "NOTICE", "GO_LICENSE"} {
		if err = write("runtime-notices/"+name, legal.Text(name)); err != nil {
			return nil, err
		}
	}
	if err = write("manifest.json", manifest); err != nil {
		return nil, err
	}
	if err = write("index.html", []byte(page)); err != nil {
		return nil, err
	}
	for _, a := range pkg.Assets {
		if err = write("assets/"+a.ID, a.Data); err != nil {
			return nil, err
		}
	}
	if err = w.Close(); err != nil {
		return nil, err
	}
	if buffer.Len() > model.MaxPackageBytes {
		return nil, errors.New("export_too_large")
	}
	return buffer.Bytes(), nil
}
func JSON(p model.Project) ([]byte, error) {
	return json.MarshalIndent(model.Package{Format: "playfielder", Version: 1, Title: p.Title, Content: p.Content, Assets: p.Assets}, "", "  ")
}
func DescribeError(err error) string {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "package_incomplete"
	}
	return fmt.Sprint(err)
}
