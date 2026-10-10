/* Browser glue only. Narrative transitions execute the shared Go/Wasm engine. */
(() => {
  'use strict';
  const config = window.__pfConfig;
  const send = (type, data = {}) => parent.postMessage({type, token:config.token, ...data}, '*');
  const esc = value => CSS.escape(String(value));
  let state = {events:[], activated:[], fired:[], pending:[], messages:[], choices:{}, unlocked:[], read:[]};
  let replies = {}, engineReady = false, queue = [];
  let saved;
  try { saved = localStorage.getItem('playfielder:' + config.saveKey); if (saved) state = JSON.parse(saved); } catch {}
  window.addEventListener('message', event => {
    if (event.source !== parent || event.data?.token !== config.token || event.data.type !== 'restore') return;
    if (engineReady) { state = event.data.state; step({type:'tick'}); }
    else saved = JSON.stringify(event.data.state);
  });
  const persist = () => { try {localStorage.setItem('playfielder:' + config.saveKey, JSON.stringify(state));} catch {} send('state', {state}); };
  const render = () => {
    for (const id of state.unlocked) document.querySelectorAll('[data-story-lock="'+esc(id)+'"]').forEach(el=>el.hidden=false);
    const panel = document.getElementById('pf-story-panel');
    if (!panel) return;
    panel.replaceChildren();
    const heading = document.createElement('strong'); heading.textContent = '剧情来信'; panel.append(heading);
    for (const message of state.messages) {
      const node = config.flow.nodes.find(n=>n.id===message.nodeId); if (!node) continue;
      const box=document.createElement('div');box.className='pf-message';
      const actor=config.flow.characters.find(a=>a.id===node.characterId);
      const name=document.createElement('small');name.textContent=message.replyId?'你的回复':(actor?.name||'来信');box.append(name);
      const text=document.createElement('p');text.textContent=message.replyId?node.replies.find(r=>r.id===message.replyId)?.text:node.text;box.append(text);
      if(!message.replyId) for(const reply of replies[node.id]||[]){const button=document.createElement('button');button.textContent=reply.text;button.addEventListener('click',()=>step({type:'reply',nodeId:node.id,replyId:reply.id}));box.append(button)}
      panel.append(box);
    }
    panel.hidden=!state.messages.length;
  };
  function step(action) {
    if (!engineReady) {queue.push(action);return;}
    try {
      const out=JSON.parse(window.playfielderStoryStep(JSON.stringify({flow:config.flow,state,action,now:Date.now()})));
      if(out.error){send('error',{message:out.error});return;}
      const changed=JSON.stringify(state)!==JSON.stringify(out.state);state=out.state;replies=out.replies;render();if(changed)persist();
    } catch {send('error',{message:'story_runtime_failed'});}
  }
  window.playfielder = Object.freeze({event:name=>{if(typeof name==='string')step({type:'event',event:name});}});
  const showPage = id => document.querySelectorAll('[data-page]').forEach(el=>el.hidden=el.dataset.page!==id);
  document.addEventListener('click', event => {
    const page=event.target.closest('[data-page-link]');if(page){event.preventDefault();showPage(page.dataset.pageLink)}
    const trigger=event.target.closest('[data-story-event]');if(trigger)window.playfielder.event(trigger.dataset.storyEvent);
    const link=event.target.closest('a');if(link)event.preventDefault();
    if(config.edit){const el=event.target.closest('[data-wb-node]');if(el){event.preventDefault();event.stopPropagation();document.querySelectorAll('[data-pf-selected]').forEach(n=>n.removeAttribute('data-pf-selected'));el.setAttribute('data-pf-selected','');send('select',{id:el.dataset.wbNode,tag:el.tagName,text:el.textContent.slice(0,24000),src:el.getAttribute('src')||''})}}
  }, true);
  document.addEventListener('submit', e=>e.preventDefault());
  window.addEventListener('error', e=>send('error',{message:String(e.message).slice(0,300)}));
  window.addEventListener('unhandledrejection', ()=>send('error',{message:'unhandled_promise_rejection'}));
  if(config.edit){const style=document.createElement('style');style.textContent='[data-wb-node]{cursor:crosshair}[data-pf-selected]{outline:2px solid #c78c42!important;outline-offset:3px}';document.head.append(style)}
  const pages=[...document.querySelectorAll('[data-page]')];if(pages.length)showPage(pages[0].dataset.page);
  if(config.flow.nodes.length) {
    const style=document.createElement('style');style.textContent='#pf-story-panel{position:fixed;right:12px;bottom:12px;width:min(320px,calc(100vw - 24px));max-height:50vh;overflow:auto;background:#fff7e9;color:#27241f;box-shadow:0 10px 40px #0004;padding:18px;border-radius:12px;z-index:2147483646;font:14px/1.6 system-ui}.pf-message{border-top:1px solid #ddd;padding-top:10px;margin-top:10px}.pf-message p{white-space:pre-wrap}.pf-message button{display:block;margin:6px 0;padding:8px 12px;border:1px solid #bda47c;background:white;color:#27241f;border-radius:6px}';document.head.append(style);
    const panel=document.createElement('aside');panel.id='pf-story-panel';panel.hidden=true;document.body.append(panel);
    const go=new Go();
    const bytes=Uint8Array.from(atob(window.__pfWasm),c=>c.charCodeAt(0));
    WebAssembly.instantiate(bytes,go.importObject).then(result=>{
      go.run(result.instance).catch(()=>send('error',{message:'story_engine_stopped'}));
      engineReady=true;
      if(saved){try{state=JSON.parse(saved)}catch{}}
      step({type:'event',event:'start'});for(const action of queue)step(action);queue=[];
      setInterval(()=>step({type:'tick'}),500);
      send('ready',{story:true});
    }).catch(()=>send('error',{message:'story_wasm_unavailable'}));
  } else send('ready',{story:false});
})();
