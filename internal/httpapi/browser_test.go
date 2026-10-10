package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Azhe-ZJ/Playfielder/internal/browser"
	"github.com/chromedp/chromedp"
)

// Opt-in acceptance uses a new Chrome profile and temporary application data.
func TestBrowserEditorAndStory(t *testing.T) {
	if os.Getenv("PLAYFIELDER_BROWSER_TEST") != "1" {
		t.Skip("explicit browser acceptance only")
	}
	server, _, _ := testApp(t)
	ctx, stop := context.WithTimeout(context.Background(), 60*time.Second)
	defer stop()
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browser.Executable()), chromedp.Flag("no-sandbox", false))
	allocator, stopAllocator := chromedp.NewExecAllocator(ctx, options...)
	defer stopAllocator()
	ctx, closeBrowser := chromedp.NewContext(allocator)
	defer closeBrowser()
	do := func(actions ...chromedp.Action[chromedp.Void]) {
		t.Helper()
		if e := chromedp.Do(ctx, actions...); e != nil {
			t.Fatal(e)
		}
	}
	poll := func(expression string) {
		t.Helper()
		if _, e := chromedp.Run(ctx, chromedp.Poll[bool](expression, chromedp.WithPollingTimeout(12*time.Second))); e != nil {
			t.Fatal(expression, e)
		}
	}
	do(chromedp.EmulateViewport(1440, 1000), chromedp.Navigate(server.URL), chromedp.WaitVisible(chromedp.ID("new-project")), chromedp.Click(chromedp.ID("new-project")), chromedp.SetValue(chromedp.ID("new-title"), "隔离浏览器验收"), chromedp.Click(chromedp.CSS("#new-form button[type=submit]")))
	poll(`!document.getElementById('workspace').hidden && document.getElementById('preview-loading').hidden`)
	// CDP attaches to the opaque preview target; the application itself cannot.
	frameContext := func() (context.Context, context.CancelFunc) {
		t.Helper()
		infos, e := chromedp.Targets(ctx)
		if e != nil {
			t.Fatal(e)
		}
		for _, info := range infos {
			if info.Type == "iframe" {
				return chromedp.NewContext(ctx, chromedp.WithTargetID(info.TargetID), chromedp.WithDetachOnCancel())
			}
		}
		t.Fatal("opaque preview target missing")
		return nil, nil
	}
	frame, closeFrame := frameContext()
	if e := chromedp.Do(frame, chromedp.Click(chromedp.CSS("h1"))); e != nil {
		t.Fatal(e)
	}
	closeFrame()
	poll(`!document.getElementById('inspector').hidden`)
	do(chromedp.SetValue(chromedp.ID("selection-text"), "浏览器修改后的标题"), chromedp.Click(chromedp.ID("apply-text")))
	poll(`document.getElementById('source-html').value.includes('浏览器修改后的标题') && document.getElementById('preview-loading').hidden`)
	do(chromedp.Click(chromedp.ID("mode-play")))
	poll(`document.getElementById('preview-loading').hidden && document.getElementById('mode-play').classList.contains('active')`)
	frame, closeFrame = frameContext()
	if e := chromedp.Do(frame, chromedp.Click(chromedp.CSS("[data-page-link=archive]")), chromedp.SetValue(chromedp.ID("answer"), "217"), chromedp.Click(chromedp.ID("check"))); e != nil {
		t.Fatal(e)
	}
	if _, e := chromedp.Run(frame, chromedp.Poll[bool](`document.querySelector('#pf-story-panel button')!==null`, chromedp.WithPollingTimeout(10*time.Second))); e != nil {
		t.Fatal(e)
	}
	if e := chromedp.Do(frame, chromedp.Click(chromedp.CSS("#pf-story-panel button"))); e != nil {
		t.Fatal(e)
	}
	closeFrame()
	poll(`JSON.parse(localStorage.getItem('pf-preview:'+location.hash.split('/')[1])).unlocked.includes('letter')`)
	do(chromedp.Click(chromedp.ID("refresh-preview")))
	poll(`document.getElementById('preview-loading').hidden`)
	frame, closeFrame = frameContext()
	if _, e := chromedp.Run(frame, chromedp.Poll[bool](`!document.querySelector('[data-story-lock=letter]').hidden`, chromedp.WithPollingTimeout(10*time.Second))); e != nil {
		t.Fatal("progress lost after refresh", e)
	}
	closeFrame()
	// Verify a source save and a restored undo through the same public UI.
	do(chromedp.Click(chromedp.ID("undo")))
	poll(`document.getElementById('source-html').value.includes('失踪的第七封信')`)
	do(chromedp.Click(chromedp.ID("redo")))
	poll(`document.getElementById('source-html').value.includes('浏览器修改后的标题')`)
	poll(`document.getElementById('preview-loading').hidden`)
	if dir := os.Getenv("PLAYFIELDER_ACCEPTANCE_OUTPUT"); dir != "" {
		if e := os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		for _, width := range []int64{1440, 390} {
			do(chromedp.EmulateViewport(width, 1000))
			shot, e := chromedp.Run(ctx, chromedp.FullScreenshot(90))
			if e != nil {
				t.Fatal(e)
			}
			name := "desktop.png"
			if width == 390 {
				name = "mobile.png"
			}
			if e = os.WriteFile(filepath.Join(dir, name), shot, 0644); e != nil {
				t.Fatal(e)
			}
		}
	}
}
