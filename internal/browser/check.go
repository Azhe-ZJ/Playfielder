// Package browser performs optional technical checks in a fresh, sandboxed
// Chromium process. It does not judge narrative quality or prove a full playthrough.
package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/Azhe-ZJ/Playfielder/internal/document"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

type Check struct {
	Width    int      `json:"width"`
	Ready    bool     `json:"ready"`
	Isolated bool     `json:"isolated"`
	Errors   []string `json:"errors"`
}

func Executable() string {
	if configured := os.Getenv("PLAYFIELDER_CHROME"); configured != "" {
		return configured
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome"} {
		if p, e := exec.LookPath(name); e == nil {
			return p
		}
	}
	path := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, e := os.Stat(path); e == nil {
		return path
	}
	return ""
}
func Validate(ctx context.Context, page string) ([]Check, error) {
	path := Executable()
	if path == "" {
		return nil, errors.New("chromium_not_installed")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(path), chromedp.Flag("no-sandbox", false), chromedp.Flag("disable-background-networking", true))
	allocator, stop := chromedp.NewExecAllocator(ctx, options...)
	defer stop()
	browser, closeBrowser := chromedp.NewContext(allocator)
	defer closeBrowser()
	encoded, _ := json.Marshal(page)
	out := []Check{}
	for _, width := range []int{1280, 390} {
		var check Check
		script := `document.body.replaceChildren();window.pfProbe={ready:false,errors:[]};const frame=document.createElement('iframe');frame.sandbox='allow-scripts';frame.style='width:100%;height:800px;border:0';window.addEventListener('message',event=>{if(event.source!==frame.contentWindow)return;if(event.data.type==='ready')window.pfProbe.ready=true;if(event.data.type==='error')window.pfProbe.errors.push(String(event.data.message).slice(0,200));});document.body.append(frame);frame.srcdoc=` + string(encoded) + `;`
		err := chromedp.Do(browser, chromedp.EmulateViewport(int64(width), 900), chromedp.Navigate("about:blank"), chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			if _, e := chromedp.Call(ctx, network.Enable, network.EnableParams{}); e != nil {
				return e
			}
			patterns := []*network.BlockPattern{}
			for _, pattern := range []string{"http://*/*", "https://*/*", "file:///*", "ws://*/*", "wss://*/*"} {
				patterns = append(patterns, &network.BlockPattern{URLPattern: pattern, Block: true})
			}
			_, e := chromedp.Call(ctx, network.SetBlockedURLs, network.SetBlockedURLsParams{URLPatterns: patterns})
			return e
		}), chromedp.Evaluate[chromedp.Void](script), chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, e := chromedp.Run(ctx, chromedp.Poll[bool](`window.pfProbe.ready || window.pfProbe.errors.length>0`, chromedp.WithPollingTimeout(15*time.Second)))
			return e
		}))
		if err == nil {
			check, err = chromedp.Run(browser, chromedp.Evaluate[Check](`({width:innerWidth,ready:window.pfProbe.ready,errors:window.pfProbe.errors,isolated:(()=>{try{void document.querySelector('iframe').contentWindow.document;return false}catch{return true}})()})`))
		}
		if err != nil {
			return out, errors.New("browser_check_failed")
		}
		out = append(out, check)
	}
	return out, nil
}
func CheckContent(ctx context.Context, o document.Options) ([]Check, error) {
	page, e := document.Build(o)
	if e != nil {
		return nil, e
	}
	return Validate(ctx, page)
}
