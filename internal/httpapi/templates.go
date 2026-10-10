package httpapi

import "github.com/Azhe-ZJ/Playfielder/internal/model"

// Synthetic examples written for this independent edition. No original game
// text, images, fonts or production data are distributed.
func Template(name string) model.Content {
	s := model.Source{HTML: `<main><p class="eyebrow">PLAYFIELDER · 调查档案</p><h1>失踪的第七封信</h1><p>凌晨 02:17，旧档案馆收到一封没有寄件人的信。信封里只有一张纸条：</p><blockquote>“灯熄灭之前，请找回第七封信。”</blockquote><button data-page-link="archive">打开档案</button></main><section data-page="archive"><h2>档案 017</h2><p>记录员在最后一页留下了三个数字：2、1、7。</p><label>输入档案编号 <input id="answer" autocomplete="off"></label><button id="check">核对编号</button><p id="result" aria-live="polite"></p><div data-story-lock="letter" hidden><h3>第七封信</h3><p>你找到的并不是求救信，而是一封迟到了十年的道歉。</p></div></section>`, CSS: `:root{color-scheme:light}*{box-sizing:border-box}body{margin:0;background:#f4efe4;color:#282a25;font:17px/1.9 Georgia,serif}main,section{max-width:680px;margin:60px auto;padding:0 28px}h1{font-size:clamp(32px,7vw,52px);line-height:1.25}h2{font-size:30px}.eyebrow{font:11px/1.5 system-ui;letter-spacing:.22em;color:#798171}blockquote{margin:30px 0;border-left:3px solid #8b967e;padding:12px 24px;background:#e9e5d8}button,input{font:inherit;padding:10px 18px;border:1px solid #b7bdad;border-radius:6px}button{background:#3c4939;color:#fff;cursor:pointer}input{width:130px;background:#fffdf7}label{display:block;margin:20px 0}[hidden]{display:none!important}`, JavaScript: `document.getElementById('check').addEventListener('click',()=>{const solved=document.getElementById('answer').value.trim()==='217';document.getElementById('result').textContent=solved?'编号正确。档案柜缓缓打开。':'编号不匹配，再看看记录员留下的数字。';if(solved)window.playfielder.event('archive-opened')});`}
	// The opening page participates in the built-in page navigation convention.
	s.HTML = `<div data-page="intro">` + s.HTML[:indexSection(s.HTML)] + `</div>` + s.HTML[indexSection(s.HTML):]
	story := model.Story{Version: 1, Characters: []model.Character{{ID: "archivist", Name: "档案管理员"}}, Nodes: []model.StoryNode{{ID: "welcome", CharacterID: "archivist", Text: "你收到的信属于档案 017。请独立调查，线索就在页面里。", Event: "start", DelayMS: 0, Replies: []model.Reply{}}, {ID: "found", CharacterID: "archivist", Text: "谢谢你找回它。你愿意打开这封信吗？", Event: "archive-opened", DelayMS: 700, Replies: []model.Reply{{ID: "open-letter", Text: "打开第七封信", Effects: []model.Effect{{Type: "unlock", Target: "letter"}}}}}}}
	if name == "blank" {
		return model.Content{Source: model.Source{HTML: `<main><h1>你的故事从这里开始</h1><p>点击文字进行编辑，或在左侧描述你的想法。</p></main>`, CSS: s.CSS}, Story: model.EmptyStory()}
	}
	return model.Content{Source: s, Story: story}
}
func indexSection(s string) int {
	for i := 0; i+8 <= len(s); i++ {
		if s[i:i+8] == "<section" {
			return i
		}
	}
	return len(s)
}
