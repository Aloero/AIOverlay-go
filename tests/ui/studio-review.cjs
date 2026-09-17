'use strict';
// Follow-up acceptance checks: real Go handlers, no provider calls or capture.
const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const base=process.env.TEST_SERVER,token=process.env.TEST_TOKEN;
const root=path.resolve(__dirname,'../..'),fixture=JSON.parse(fs.readFileSync(path.join(root,'examples/config.example.json')));
const results=[],calls=[],errors=[];let browser,page;
async function api(p,method='GET',body){const r=await fetch(base+p,{method,headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});if(!r.ok)throw Error(await r.text());return r.json();}
async function reset(){const c=structuredClone(fixture);c.control_center.listen_address=new URL(base).host;await api('/api/config','PUT',c);await page.goto(base);await page.waitForSelector('#poolOrder');await page.waitForTimeout(150);}
async function test(name,fn){try{await reset();await fn();results.push({test:name,passed:true});}catch(e){results.push({test:name,passed:false,error:e.message});}}
(async()=>{try{
 browser=await chromium.launch({headless:true});page=await browser.newPage({viewport:{width:1024,height:768}});page.setDefaultTimeout(6000);page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept());
 await page.exposeFunction('__reviewDispatch',async r=>{calls.push(structuredClone(r));if(r.path==='/desktop/close')return {};if(r.method==='POST'&&['/api/job','/api/run','/api/benchmark','/api/providers/test','/api/vpn/probe','/api/capture'].includes(r.path))throw Error('TEST FORBIDS NETWORK/CAPTURE');return api(r.path,r.method,r.body==null?undefined:r.body);});
 await page.addInitScript(()=>{window.studioDispatch=r=>{window.__reviewRequests||=[];window.__reviewRequests.push(r.path);const value=r.path==='/api/prompts/last'&&window.__promptFixture?Promise.resolve(window.__promptFixture):window.__reviewDispatch(r);return value.then(data=>window.__studioReply(r.id,{ok:true,data}),e=>window.__studioReply(r.id,{ok:false,error:e.message}));};});
 await test('SaveKeyboardCommitsFocusedInput',async()=>{await page.fill('#pipelineName','Focused title');await page.locator('#pipelineName').press('Control+s');await page.waitForTimeout(220);assert.equal((await api('/api/config')).pipelines[0].name,'Focused title');});
 await test('ApplyIncludesEditedEdge',async()=>{await page.evaluate(()=>{state.edge=pipe().edges[0].id;renderInspector();});await page.locator('#edgePriority').evaluate(e=>e.closest('details').open=true);await page.fill('#edgePriority','57');await page.locator('#edgePriority').press('Control+s');await page.waitForTimeout(220);assert.equal((await api('/api/config')).pipelines[0].edges[0].priority,57);});
 await test('TimeoutLabelUpdatesWithValue',async()=>{await page.click('[data-tab="budget"]');await page.fill('#bFirst','12345');await page.locator('#bFirst').press('Tab');assert.match(await page.locator('#bFirst').locator('..').textContent(),/12\.345 с/);});
 await test('VisionQueueUsesItsOwnCursor',async()=>{await page.click('#enableVision');const id=await page.evaluate(()=>state.node);await page.click('#visionPlan');await page.waitForSelector('#queueRows');const r=calls.findLast(r=>r.path==='/api/pool/plan');assert.equal(r.body.node_id,id+'/vision');});
 await test('RunSummaryHonoursFinalGraphFailure',async()=>{const html=await page.evaluate(()=>runSummaryMarkup({outcome:'validation_failed',error:'fixture rejected',failure_node_id:'validate',events:[],winner:null}));assert.match(html,/Ответ не получен/);assert.match(html,/fixture rejected/);});
 await test('SummaryLinksToFailedEdgeAndConnection',async()=>{const html=await page.evaluate(()=>runSummaryMarkup({outcome:'auth_failed',error:'fixture',failure_node_id:'solver',pipeline_id:'p',events:[{kind:'attempt_start',node_id:'solver',at_ms:0,attempt:{id:'a',provider_id:'gemini',model_id:'test'}},{kind:'attempt_end',node_id:'solver',at_ms:1,attempt:{id:'a',provider_id:'gemini',model_id:'test'},outcome:'auth_failed'},{kind:'transition',node_id:'solver',edge_id:'fallback',outcome:'auth_failed',message:'reserve'}]}, {error:'fixture'}));assert.match(html,/data-provider-id="gemini"/);assert.match(html,/data-edge-id="fallback"/);});

 await test('LastPromptDataRequiresExplicitAction',async()=>{
  await page.click('[data-tab="prompts"]');await page.click('#promptPreview');await page.waitForSelector('#previewSystem');
  assert.equal(await page.evaluate(()=>window.__reviewRequests.includes('/api/prompts/last')),false);
  await page.evaluate(()=>{window.__promptFixture={run_id:'fixture-run',pipeline_id:pipe().id,items:[{node_id:state.node,provider_id:'fixture',model_id:'model',image_attached:false,truncated:false,values:{input_text:'PRIVATE TASK Q4',previous_output:'210',vision_text:'Q4 = 210'}}]};});
  await page.click('#loadLastPrompt');await page.waitForFunction(()=>document.querySelector('#previewInput').value==='PRIVATE TASK Q4');
  assert.match(await page.locator('#lastPromptInfo').textContent(),/Изображение не прикладывалось/);
  await page.click('#closeDialog');await page.click('#promptPreview');await page.waitForSelector('#previewInput');assert.equal(await page.locator('#previewInput').inputValue(),'input text');
 });
 await test('PreciseHistoryActionsDoNotActivateAnotherPipeline',async()=>{
  const before=await page.evaluate(()=>state.config.active_pipeline_id);
  await page.evaluate(()=>openSettingTarget({page:'providers',providerID:'opencode'}));await page.waitForSelector('#bulkKeys');
  assert.equal(await page.evaluate(()=>state.config.active_pipeline_id),before);await page.click('#closeDialog');
  await page.evaluate(()=>openSettingTarget({page:'pipelines',pipelineID:pipe().id,edgeID:pipe().edges[0].id}));await page.waitForSelector('#edgeMode');assert.equal(await page.evaluate(()=>state.edge),await page.evaluate(()=>pipe().edges[0].id));
 });
 await test('EdgeDraftSurvivesUndoRedo',async()=>{
  const original=await page.evaluate(()=>pipe().edges[0].priority);
  await page.evaluate(()=>{state.edge=pipe().edges[0].id;renderInspector();});await page.locator('#edgePriority').evaluate(e=>e.closest('details').open=true);
  await page.fill('#edgePriority','57');await page.locator('#edgePriority').press('Tab');await page.click('#undoDraft');assert.equal(await page.evaluate(()=>pipe().edges[0].priority),original);
  await page.click('#redoDraft');assert.equal(await page.evaluate(()=>pipe().edges[0].priority),57);
 });
 await test('ReserveStagesAreSmallerThanMainRoute',async()=>{
  await page.evaluate(()=>{pipe().nodes.push({id:'reserve',name:'Reserve',type:'model_pool',model_pool:copy(pipe().nodes[1].model_pool),position:{x:400,y:400}});pipe().edges.push({id:'r',from:pipe().nodes[1].id,to:'reserve',on:['any_error'],priority:1,max_visits:1});renderGraph();});
  const sizes=await page.evaluate(()=>({main:document.querySelector('.primary-stage.model_pool').offsetWidth,reserve:document.querySelector('.reserve-stage').offsetWidth}));assert.ok(sizes.reserve<sizes.main);
  assert.match(await page.locator('.reserve-marker').first().textContent(),/Резерв/);
 });
 await test('ActualRecentModelsAppearWithoutManualSelection',async()=>{
  const ref=await page.evaluate(()=>{const m=pipe().nodes[1].model_pool.models[0];return modelRef(m)});
  await page.evaluate(ref=>{workspaceInfo={...(workspaceInfo||{}),recent_models:[ref]};state.config.control_center.recent_models=[];},ref);
  await page.click('#addSolverModel');await page.click('[data-catalog-group="recent"]');await page.waitForSelector('#catalogRows .ref');assert.ok((await page.locator('#catalogRows .ref').allTextContents()).includes(ref));
 });
 await test('ExpandedJournalDetailsSurvivePolling',async()=>{
  await page.evaluate(()=>{state.job={id:'fixture',running:false,trace:{outcome:'success',events:[{kind:'effective_limits',at_ms:1,message:'fixture limits'}]}};state.page='runs';renderPage();const root=document.querySelector('#liveRun');root.querySelector('.full-trace').open=true;root.querySelector('.trace-detail').open=true;renderLiveRun();});
  assert.equal(await page.locator('#liveRun .full-trace').evaluate(e=>e.open),true);assert.equal(await page.locator('#liveRun .trace-detail').evaluate(e=>e.open),true);
 });
 await test('UntriedQueueAndRetryCountAreVisible',async()=>{
  const html=await page.evaluate(()=>runSummaryMarkup({outcome:'cancelled',error:'budget',budget:{max_model_calls:2},events:[{kind:'pool_end',node_id:'solver',candidate_count:14,attempted_models:2,untried_models:12},{kind:'budget_exhausted',budget_scope:'model_calls'}]}));assert.match(html,/12 из 14/);assert.match(html,/общий бюджет/);
 });
 await test('CompactLayoutHasNoPageOverflow',async()=>{
  for(const size of [{width:820,height:614},{width:1024,height:768},{width:1320,height:880}]){await page.setViewportSize(size);await page.waitForTimeout(60);const m=await page.evaluate(()=>({width:document.documentElement.scrollWidth,visible:innerWidth,height:document.documentElement.scrollHeight,screen:innerHeight}));assert.ok(m.width<=m.visible&&m.height<=m.screen,JSON.stringify(m));}
  const dir=process.env.ARTIFACT_DIR||path.join(os.tmpdir(),'aio028-review');fs.mkdirSync(dir,{recursive:true});await page.screenshot({path:path.join(dir,'review-studio.png')});
 });
 assert.equal(errors.length,0,errors.join('; '));
 console.log(JSON.stringify({results,js_errors:errors,live_model_requests:0,network_probes:0,physical_windows:false},null,2));process.exitCode=results.some(r=>!r.passed)?1:0;
}finally{await browser?.close();}})().catch(e=>{console.error(e);process.exitCode=1;});
