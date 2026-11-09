'use strict';
const fs=require('fs'),path=require('path');
const root=path.resolve(__dirname,'../..');
const {JSDOM,VirtualConsole}=require(path.join(root,'tests/ui/node_modules/jsdom'));
const fixture=JSON.parse(fs.readFileSync(path.join(root,'examples/config.example.json'),'utf8'));
const pause=ms=>new Promise(r=>setTimeout(r,ms));
let source=fs.readFileSync(path.join(root,'web/static/index.html'),'utf8');
source=source.replace(/<script src="\/assets\/([^"/]+)" defer><\/script>/g,(_,n)=>'<script>'+fs.readFileSync(path.join(root,'web/static',n),'utf8')+'</script>');
async function session(config=structuredClone(fixture)) {
 const calls=[],errors=[],held=[],heldImports=[];let hold=false,holdImport=false;
 const vc=new VirtualConsole();vc.on('jsdomError',e=>errors.push(e.message));
 const dom=new JSDOM(source,{url:'https://desktop.invalid',runScripts:'dangerously',pretendToBeVisual:true,virtualConsole:vc,beforeParse(w){
  w.confirm=()=>true;w.ResizeObserver=class{observe(){}disconnect(){}};w.HTMLDialogElement.prototype.showModal=function(){this.open=true};w.HTMLDialogElement.prototype.close=function(){this.open=false};
  w.studioDispatch=r=>{calls.push(structuredClone(r));const finish=()=>{
   let data={};if((r.path==='/api/config'&&r.method==='PUT')||r.path==='/api/config/import')config=structuredClone(r.body);if(r.path==='/api/providers/config'&&r.method==='POST'){const i=config.providers.findIndex(p=>p.id===r.body.id);if(i<0)config.providers.push(structuredClone(r.body));else config.providers[i]=structuredClone(r.body);}if(r.path==='/api/config'&&r.method==='GET')data=structuredClone(config);else if(r.path.startsWith('/api/catalog'))data={items:[],total:0,offset:0,limit:100};else if(r.path==='/api/providers/catalog')data=[];else if(r.path==='/api/pool/plan')data={candidates:[],skipped:{}};
   w.__studioReply(r.id,{ok:true,data});
  };if(hold&&r.path==='/api/config'&&r.method==='PUT')held.push(finish);else if(holdImport&&r.path==='/api/config/import')heldImports.push(finish);else queueMicrotask(finish);return Promise.resolve();};
 }});
 await pause(80);return {dom,w:dom.window,calls,errors,held,heldImports,config:()=>structuredClone(config),hold:()=>hold=true,holdImport:()=>holdImport=true};
}
(async()=>{
 const results=[];
 let x=await session();
 try {
  x.hold();x.w.eval('state.config.overlay.font_size=17;dirty();');const closing=x.w.eval('closeStudio()');await pause(5);
  x.w.eval('state.config.overlay.font_size=18;dirty();');x.held.shift()();await closing;
  const close=x.calls.some(r=>r.path==='/desktop/close');const dirty=x.w.eval('state.dirty');
  results.push({test:'CloseDuringSaveKeepsNewerDraft',passed:!(close&&dirty),observed:{closed:close,unsaved:dirty,lastSubmitted:x.calls.find(r=>r.path==='/api/config'&&r.method==='PUT').body.overlay.font_size,current:x.w.eval('state.config.overlay.font_size')}});
 }finally{x.dom.window.close()}
 x=await session();
 try {
  x.w.eval('providerDialog(state.config.providers[0])');x.w.document.querySelector('#bulkKeys').value='FAKE-DIALOG-KEY';x.w.document.querySelector('#addKeys').click();x.w.__studioNativeClose();await pause(20);
  const close=x.calls.some(r=>r.path==='/desktop/close'),saved=x.calls.some(r=>r.path==='/api/providers/config');
  results.push({test:'NativeCloseProtectsProviderDraft',passed:!close||saved,observed:{closed:close,connectionSaved:saved,globalDirty:x.w.eval('state.dirty')}});
 }finally{x.dom.window.close()}

 for(const choice of ['save','discard','cancel']){
  x=await session();
  try{
   x.w.eval('providerDialog(state.config.providers[0])');x.w.document.querySelector('#bulkKeys').value='FAKE-MODAL-KEY';x.w.document.querySelector('#addKeys').click();x.w.__studioNativeClose();await pause(20);
   const button=x.w.document.querySelector('#unsavedChoice [data-choice="'+choice+'"]');if(!button)throw Error('missing unsaved choices');button.click();await pause(40);
   const closed=x.calls.some(r=>r.path==='/desktop/close'),saved=x.calls.some(r=>r.path==='/api/providers/config');
   results.push({test:'ProviderDraftChoice_'+choice,passed:choice==='save'?closed&&saved:choice==='discard'?closed&&!saved:!closed&&!saved});
  }finally{x.dom.window.close()}
 }
 const cfg=structuredClone(fixture);const p=cfg.pipelines[0].nodes.find(n=>n.model_pool).model_pool;p.models=null;p.selection={mode:'catalog',provider_ids:[],free_only:true,modality:'auto',default_quality_score:0};p.quality_floor=0;
 x=await session(cfg);
 try {
  const text=x.w.document.querySelector('#page').textContent;
  results.push({test:'CatalogPoolWithNullModelsLoads',passed:!text.includes('Не удалось'),observed:text.slice(0,300)});
 }finally{x.dom.window.close()}

 // A completed older PUT must never overwrite a subsequently imported file.
 x=await session();
 try {
  x.hold();x.w.eval('state.config.overlay.font_size=17;dirty();');const saving=x.w.eval('saveConfig()');await pause(5);
  const imported=x.config();imported.overlay.font_size=21;
  const importing=x.w.importConfig({target:{files:[{size:1,text:async()=>JSON.stringify(imported)}],value:'fixture.json'}});await pause(5);
  const waited=!x.calls.some(r=>r.path==='/api/config/import');
  x.held.shift()();await saving;await importing;await pause(10);
  results.push({test:'ImportWaitsForPendingSave',passed:waited&&x.config().overlay.font_size===21&&x.w.eval('state.config.overlay.font_size')===21});
 }finally{x.dom.window.close()}
 x=await session();
 try {
  x.holdImport();const imported=x.config();imported.overlay.font_size=21;
  const importing=x.w.importConfig({target:{files:[{size:1,text:async()=>JSON.stringify(imported)}],value:'fixture.json'}});await pause(5);
  x.w.eval('state.config.overlay.font_size=23;dirty();');const saving=x.w.eval('saveConfig()');await pause(5);
  const waited=!x.calls.some(r=>r.path==='/api/config'&&r.method==='PUT');
  x.heldImports.shift()();await importing;await saving;await pause(10);
  results.push({test:'SaveWaitsForImportAndKeepsNewerDraft',passed:waited&&x.config().overlay.font_size===23&&x.w.eval('state.config.overlay.font_size')===23&&!x.w.eval('state.dirty')});
 }finally{x.dom.window.close()}
 x=await session();
 try {
  x.holdImport();const imported=x.config();imported.overlay.font_size=21;
  const importing=x.w.importConfig({target:{files:[{size:1,text:async()=>JSON.stringify(imported)}],value:'fixture.json'}});await pause(5);
  x.w.eval('state.config.overlay.font_size=23;dirty();');x.heldImports.shift()();await importing;await pause(10);
  results.push({test:'ImportPreservesNewerUnsavedDraft',passed:x.config().overlay.font_size===21&&x.w.eval('state.config.overlay.font_size')===23&&x.w.eval('state.dirty')});
 }finally{x.dom.window.close()}
 console.log(JSON.stringify({results,actualWindows:false,liveModelRequests:0},null,2));
 process.exitCode=results.some(r=>!r.passed)?1:0;
})().catch(e=>{console.error(e);process.exitCode=1});
