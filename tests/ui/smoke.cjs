'use strict';
// DOM integration tests for the embedded JS binding. The real Go HTTP handlers
// are used in developer mode on TEST_SERVER; no model inference is performed.
const {JSDOM,VirtualConsole}=require('jsdom');
const fs=require('node:fs');const path=require('node:path');const assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../..');const base=process.env.TEST_SERVER||'http://127.0.0.1:18723';
let html=fs.readFileSync(path.join(root,'web/static/index.html'),'utf8');
html=html.replace(/<link rel="stylesheet" href="\/assets\/([^"/]+)">/g,(_,name)=>'<style>'+fs.readFileSync(path.join(root,'web/static',name),'utf8')+'</style>');
html=html.replace(/<script src="\/assets\/([^"/]+)" defer><\/script>/g,(_,name)=>'<script>'+fs.readFileSync(path.join(root,'web/static',name),'utf8')+'</script>');
const errors=[],calls=[];let checks=0;
const vc=new VirtualConsole();vc.on('jsdomError',e=>{if(!e.message.includes('Could not parse CSS'))errors.push(e.message)});
const dom=new JSDOM(html,{url:'https://desktop.invalid/',runScripts:'dangerously',pretendToBeVisual:true,virtualConsole:vc,beforeParse(w){
 w.confirm=()=>true;w.ResizeObserver=class{observe(){} disconnect(){}};
 w.HTMLDialogElement.prototype.showModal=function(){this.open=true};w.HTMLDialogElement.prototype.close=function(){this.open=false};
 w.SVGElement.prototype.getBBox=()=>({x:0,y:0,width:100,height:60});
 w.studioDispatch=r=>{
  calls.push({path:r.path,method:r.method});
  (async()=>{
   try{
    let data;
    if(r.method==='POST'&&['/api/job','/api/benchmark','/api/providers/test','/api/vpn/probe'].includes(r.path))throw Error('TEST FORBIDS EXTERNAL REQUESTS');
    if(r.path==='/desktop/close')data={};
    else if(r.path==='/api/capture')data={image_base64:'dGVzdA==',bytes:4,duration_ms:12,method:'Fixture capture (not Windows)'};
    else {const res=await fetch(base+r.path,{method:r.method,headers:{'Content-Type':'application/json',Authorization:'Bearer '+process.env.TEST_TOKEN},body:r.body===null||r.body===undefined?undefined:JSON.stringify(r.body)});if(!res.ok)throw Error(await res.text());data=await res.json()}
    w.__studioReply(r.id,{ok:true,data});
   }catch(e){w.__studioReply(r.id,{ok:false,error:e.message})}
  })();return Promise.resolve();
 };
}});
const w=dom.window,$=s=>w.document.querySelector(s);
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function wait(f,message){const end=Date.now()+5000;while(!f()){if(Date.now()>end)throw Error(message+'; JS errors: '+errors.join('; '));await pause(15)}}
function click(s){assert.ok($(s),'missing '+s);$(s).click()}
function change(s,v){$(s).value=v;$(s).dispatchEvent(new w.Event('change',{bubbles:true}))}
function ok(c,m){assert.ok(c,m);checks++}
(async()=>{
 try{
  await wait(()=>$('#solverModels'),'pipeline did not load');
  ok($('#appliedSummary').textContent.includes('Следующий снимок:'),'applied pipeline state is visible');
  ok(w.eval("runtimeAudioStateParts({transport_state:'alive',signal_state:'silent'}).join(' · ')").includes('transport жив'),'audio runtime transport state is explicit');
  ok(w.eval("runtimeAudioStateParts({transport_state:'alive',signal_state:'silent'}).join(' · ')").includes('WASAPI: тишина'),'audio silent packet is distinct from no packets');
  click('#runtimeExplain');await wait(()=>w.document.querySelector('[data-behavior-report]'),'behavior report');
  ok($('#dialog').textContent.includes('Текущий экран'),'behavior report separates capture');
  ok($('#dialog').textContent.includes('Что получает AI'),'behavior report exposes AI inputs');
  ok(calls.some(c=>c.path==='/api/pipeline/behavior'&&c.method==='POST'),'behavior report uses pure core route');click('#closeDialog');
  ok(!errors.length,'boot has JS errors: '+errors.join(';'));
  const diagnosticState=w.runtimeSourceSummary({name:'Audio',running:true,last_diagnostic:'queue coalesced',last_error:''});
  ok(diagnosticState.includes('диагностика: queue coalesced')&&!diagnosticState.includes('ошибка:'),'bounded source diagnostic is not shown as error');
  const errorState=w.runtimeSourceSummary({name:'Audio',running:true,last_error:'device unavailable'});
  ok(errorState.includes('ошибка: device unavailable'),'source failure is labeled as error');
  const incompleteSummary=w.runSummaryMarkup({pipeline_id:'fixture',outcome:'success',events:[{kind:'context_incomplete',at_ms:0}],budget:{max_model_calls:8}},{});
  ok(incompleteSummary.includes('Контекст неполный'),'manual transcript timeout is visible in run summary');
  ok(w.traceTable([{kind:'context_incomplete',at_ms:0}]).includes('Контекст неполный'),'manual transcript timeout is named in full trace');
  click('#simulatePipeline');await wait(()=>$('#simTrigger'),'simulation trigger selector');
  ok($('#simTrigger').options.length>0,'simulation exposes configured command/source triggers');
  ok($('#dialog').textContent.includes('БЕЗ AI'),'simulation remains explicitly local');
  click('#closeDialog');
  const count=$('#solverModels').children.length;
  click('#solverModels [data-move="0,1"]');ok($('#solverModels').children.length===count,'reorder keeps models');
  click('[data-page="providers"]');await wait(()=>$('#savedConnections [data-edit-connection]'),'saved provider directory');
  const edit=w.document.querySelector('[data-edit-connection]');
  assert.ok(edit,'provider edit button');edit.click();
  await wait(()=>$('#bulkKeys'),'credential dialog');
  await wait(()=>$('#providerCapabilities')?.textContent.includes('Persistent realtime'),'provider capability matrix');
  ok($('#providerCapabilities').textContent.includes('UNSUPPORTED'),'HTTP adapter must not appear realtime-ready');
  ok(calls.some(c=>c.path==='/api/providers/capabilities'&&c.method==='GET'),'capability matrix comes from Go adapter contract');
  $('#bulkKeys').value='TEST-KEY-ONE\nTEST-KEY-TWO\\nTEST-KEY-ONE';click('#addKeys');
  ok($('#providerKeys').children.length===2,'batch key newline/dedup');ok($('#bulkKeys').value==='','key paste cleared');
  ok(!$('#providerKeys').textContent.includes('TEST-KEY-ONE'),'secret masked');
  click('#providerDone');await wait(()=>!$('#dialog').open,'connection save failed');click('#saveConfig');await wait(()=>!$('#saveState').classList.contains('dirty'),'save config failed');checks++;
  click('[data-page="pipelines"]');click('#enableVision');
  ok(!!$('#addVisionModel'),'text composite shows vision selector above LLM');
  click('#addVisionModel');await wait(()=>$('#catalogRows [data-select-model]'),'vision catalog');
  ok($('#catVision').checked&&$('#catVision').disabled,'VLM picker image filter mandatory');
  ok(!$('#catalogRows').textContent.includes('TEXT ONLY'),'VLM picker excludes text-only');
  $('#catalogRows [data-select-model]').click();await wait(()=>!$('#dialog').open,'catalog pick');
  ok($('#visionModels').children.length===1,'vision inserted');
  ok(w.document.querySelectorAll('#enableVision').length===1,'one vision switch');
  click('#saveConfig');await wait(()=>!$('#saveState').classList.contains('dirty'),'save composite failed');checks++;
  // New capture preview makes no inference calls.
  const before=calls.filter(c=>c.path==='/api/job'&&c.method==='POST').length;
  click('[data-page="settings"]');click('#settingsCapture');await wait(()=>$('#analyzePreview'),'capture preview');
  ok(calls.filter(c=>c.path==='/api/job'&&c.method==='POST').length===before,'preview sent no AI request');click('#closeDialog');
  click('[data-page="runs"]');ok(!!$('#liveRun'),'runs page');
  click('[data-page="settings"]');ok(!!$('#hotkey_analyze'),'hotkeys in embedded settings');
  ok(!!$('#vpnEnabled')&&!!$('#vpnConfig'),'AI VPN settings present');
  ok(!$('#buildExe')&&!$('#buildProduct')&&!$('#buildIcon'),'build profile is not runtime-editable');
  $('#vpnConfig').value='{"type":"vless","server":"vpn.example","server_port":443,"uuid":"fixture"}';$('#vpnConfig').dispatchEvent(new w.Event('input',{bubbles:true}));click('#vpnCheck');await wait(()=>$('#vpnStatus').textContent.includes('принят'),'VPN config validation');checks++;
  const savesBeforeClose=calls.filter(c=>c.path==='/api/config'&&c.method==='PUT').length;
  change('#fontSize',15);ok($('#saveState').classList.contains('dirty'),'close test creates unsaved draft');
  click('#closeStudio');await wait(()=>calls.some(c=>c.path==='/desktop/close'),'close unload bridge');
  ok(calls.filter(c=>c.path==='/api/config'&&c.method==='PUT').length===savesBeforeClose+1,'close saves valid draft before unload');
  ok(calls.some(c=>c.path==='/desktop/close'),'close unload uses native bridge');
  ok(!errors.length,'JS errors: '+errors.join(';'));
  console.log(JSON.stringify({passed:checks,js_errors:errors,mode:'in-process bridge simulation + real Go handlers',live_model_requests:0}));
 }finally{dom.window.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
