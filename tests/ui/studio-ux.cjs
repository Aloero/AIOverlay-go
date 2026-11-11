'use strict';
// UX interactions use the real Go handlers. Explicit network/AI buttons are
// intercepted as consent-contract fixtures and NEVER forwarded to providers.
const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const base=process.env.TEST_SERVER,token=process.env.TEST_TOKEN;if(!base)throw Error('Use tests/ui/run.cjs');
const artifacts=process.env.ARTIFACT_DIR||path.join(os.tmpdir(),'aio027-studio-ux');fs.mkdirSync(artifacts,{recursive:true});
let count=0,browser,page;const calls=[],errors=[];
const check=(v,m)=>{assert.ok(v,m);count++;};
async function cfg(){const r=await fetch(base+'/api/config',{headers:{Authorization:'Bearer '+token}});return r.json();}
async function save(){await page.click('#saveConfig');await page.waitForFunction(()=>!state.dirty&&!savePromise);}
(async()=>{
 try{
  browser=await chromium.launch({headless:true});page=await browser.newPage({viewport:{width:1024,height:768}});
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept());
  await page.exposeFunction('__uxDispatch',async r=>{
   calls.push(structuredClone(r));
   if(r.path==='/desktop/close')return {};
   if(r.path==='/api/providers/test'){check(r.body.confirm===true&&!r.body.image_base64,'explicit probe sends consent, no screenshot');return {id:'stub-consent-only'};}
   if(r.path==='/api/vpn/probe'){check(r.body.confirm===true,'VPN network probe requires consent');return {target:r.body.target,http_status:403,duration_ms:123,checked_at:new Date().toISOString()};}
   if(r.method==='POST'&&['/api/job','/api/run','/api/benchmark','/api/capture'].includes(r.path))throw Error('TEST FORBIDS INFERENCE AND CAPTURE');
   const response=await fetch(base+r.path,{method:r.method,headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},body:r.body===null||r.body===undefined?undefined:JSON.stringify(r.body)});
   if(!response.ok)throw Error(await response.text());return response.json();
  });
  await page.addInitScript(()=>{window.studioDispatch=r=>{const promise=r.path==='/api/job'&&r.method==='GET'&&window.__fixtureJob?Promise.resolve(window.__fixtureJob):window.__uxDispatch(r);return promise.then(data=>window.__studioReply(r.id,{ok:true,data}),error=>window.__studioReply(r.id,{ok:false,error:error.message}));};});
  await page.goto(base);await page.waitForSelector('#poolOrder');
  check((await page.locator('#appliedSummary').textContent()).includes('Следующий снимок:'),'applied state visible');
  await page.waitForTimeout(400);check(await page.evaluate(()=>!state.dirty),'initial viewport fit creates no dirty draft');
  check(await page.locator('#hostMode,#footerMode').count()===0,'technical chrome moved to About');
  check((await page.locator('#saveConfig').textContent()).includes('применить'),'save says apply');
  await page.selectOption('#poolOrder','round_robin');await page.selectOption('#poolPolicy','staggered_race');await save();
  let c=await cfg(),pool=c.pipelines[0].nodes.find(n=>n.model_pool).model_pool;
  check(pool.order==='round_robin'&&pool.dispatch==='staggered_race','ordering and dispatch save independently');
  const before=await page.locator('#pipelineName').inputValue();await page.fill('#pipelineName','Draft UX name');await page.locator('#pipelineName').press('Tab');
  check(!(await page.locator('#appliedSummary').textContent()).includes('Draft UX name'),'draft does not masquerade as applied pipeline');
  await page.click('#undoDraft');check(await page.locator('#pipelineName').inputValue()===before,'undo restores graph edit');
  await page.click('#redoDraft');check(await page.locator('#pipelineName').inputValue()==='Draft UX name','redo reapplies graph edit');await save();
  check((await page.locator('#appliedSummary').textContent()).includes('Draft UX name'),'successful save updates applied label');
  await page.click('#findSetting');await page.fill('#settingSearch','первый токен');await page.locator('#settingResults button').first().click();
  check(await page.locator('#bFirst').count()===1,'settings search opens first-token field');
  await page.click('[data-tab="models"]');await page.click('#solverModels [data-slot-details="0"] summary');await page.waitForFunction(()=>document.querySelector('[data-effective="0,timeout_ms"]')?.textContent.includes('с ·'));
  await page.click('[data-inherit="0,timeout_ms"]');await page.waitForFunction(()=>document.querySelector('[data-effective="0,timeout_ms"]')?.textContent.includes('наследуется'));
  check((await page.locator('[data-effective="0,timeout_ms"]').textContent()).includes('пайплайна'),'inheritance displays origin');
  await page.click('[data-inherit="0,timeout_ms"]');check(await page.locator('[data-mfield="0,timeout_ms"]').isVisible(),'override exposes field');
  await page.click('[data-inherit="0,timeout_ms"]');await save();
  await page.click('#addSolverModel');await page.waitForSelector('[data-favorite]');const favorite=await page.locator('[data-favorite]').first().getAttribute('data-favorite');await page.locator('[data-favorite]').first().click();
  await page.click('[data-catalog-group="favorites"]');await page.waitForFunction(()=>document.querySelectorAll('#catalogRows [data-favorite]').length===1);
  check(await page.locator('#catalogRows .ref').textContent()===favorite,'favorites filter entire catalogue');await page.click('#closeDialog');await save();
  c=await cfg();check(c.control_center.favorite_models.includes(favorite),'favorite stored in portable JSON');
  await page.evaluate(()=>{const p=pipe();p.nodes.push({id:'ux-reserve',type:'model_pool',name:'Reserve',position:{x:320,y:360},model_pool:defaultPool()});dirty();renderGraph();});
  check(await page.locator('[data-node="ux-reserve"]').count()===1,'reserve stage added');await page.click('#undoDraft');check(await page.locator('[data-node="ux-reserve"]').count()===0,'undo removes added graph stage');await page.click('#redoDraft');
  await page.evaluate(()=>{pipe().edges.push({id:'ux-fallback',from:'models',to:'ux-reserve',on:['any_error'],priority:10,max_visits:1});dirty();state.node='screen';state.edge=null;workspaceSettings().show_fallbacks=false;renderGraph();});
  check(await page.locator('[data-fallback-node="models"]').count()===1,'folded route retains a visible reserve marker');check(await page.locator('#graphLinks path.fallback').count()===0,'unselected fallback is folded');
  await page.click('[data-fallback-node="models"]');check(await page.locator('#graphLinks path.fallback').count()===1,'reserve marker reveals route and inspector');
  // Reserve routes must have an output edge before the draft can execute.
  await page.evaluate(()=>{pipe().edges.push({id:'ux-reserve-output',from:'ux-reserve',to:'output',on:['success'],priority:10,max_visits:1});dirty();renderGraph();});await save();
  await page.click('#simulatePipeline');await page.waitForSelector('#simulateNow');await page.click('#simulateNow');await page.waitForSelector('#simulationReport .timeline-row');
  check((await page.locator('#simulationReport').textContent()).includes('Условный результат'),'simulation has explicit hypothetical result');
  check(calls.some(r=>r.path==='/api/simulation'),'simulation reaches pure core route');
  check(!calls.some(r=>r.method==='POST'&&['/api/job','/api/run'].includes(r.path)),'simulation performs no model requests');
  await page.screenshot({path:path.join(artifacts,'simulation-1024.png')});await page.click('#closeDialog');
  await page.click('[data-page="providers"]');await page.waitForSelector('[data-test-connection="opencode"]');
  check(await page.locator('#savedConnections').count()===1,'saved connections shown first');
  await page.click('[data-test-connection="opencode"]');await page.waitForSelector('#probeConsent');
  check(await page.locator('#confirmProbe').isDisabled(),'provider test requires consent');check((await page.locator('.probe-warning').textContent()).includes('квоты'),'provider test warns about actual quota');
  await page.check('#probeConsent');await page.click('#confirmProbe');await page.waitForFunction(()=>state.page==='runs');
  check(calls.filter(r=>r.path==='/api/providers/test').length===1,'one deliberate provider test command');
  await page.click('[data-page="settings"]');await page.waitForSelector('#vpnEnabled');
  check(!(await page.locator('#vpnConfig').isVisible()),'VPN JSON not exposed in compact card');
  await page.click('#vpnEditor>summary');await page.fill('#vpnImportLink','hy2://TEST_ONLY_VPN_SECRET@vpn.example:443/?sni=vpn.example#UX-profile');await page.click('#vpnImport');await page.waitForFunction(()=>document.querySelector('#vpnProfileName')?.textContent==='UX-profile');
  check((await page.locator('#vpnConfig').inputValue()).includes('hysteria2'),'Hysteria share link converted');
  await page.click('#vpnEditor>summary');await page.waitForFunction(()=>document.querySelector('#vpnConfig')?.value==='');
  check(await page.locator('#vpnConfig').inputValue()==='','secret editor cleared on collapse');
  await page.check('#vpnEnabled');await save();await page.click('#vpnProbe');await page.waitForSelector('#vpnProbeConsent');
  check(await page.locator('#vpnProbeNow').isDisabled(),'network check separate consent');await page.check('#vpnProbeConsent');await page.click('#vpnProbeNow');await page.waitForFunction(()=>document.querySelector('#vpnProbeResult')?.textContent.includes('HTTP 403'));
  check((await page.locator('#vpnProbeResult').textContent()).includes('Доступ модели и баланс не проверялись'),'HTTP response not called successful AI auth');await page.click('#closeDialog');
  // Keep the test-only VPN disabled after consent tests; no tunnel is started.
  await page.uncheck('#vpnEnabled');await save();
  const persistedFont=(await cfg()).overlay.font_size;await page.fill('#fontSize','23');await page.locator('#fontSize').press('Tab');await page.click('#restoreDraft');await page.waitForFunction(()=>!state.dirty);
  check(Number(await page.locator('#fontSize').inputValue())===persistedFont,'restore saved discards draft only');
  await page.click('[data-page="pipelines"]');await page.waitForSelector('#canvas');
  await page.click('#toggleSidebar');check(await page.locator('#app').evaluate(el=>el.classList.contains('sidebar-collapsed')),'sidebar collapse works');await save();
  const grip=await page.locator('#inspectorResize').boundingBox(),initial=await page.locator('#inspector').evaluate(el=>el.clientWidth);
  await page.mouse.move(grip.x+2,grip.y+30);await page.mouse.down();await page.mouse.move(grip.x-35,grip.y+30,{steps:4});await page.mouse.up();check(await page.locator('#inspector').evaluate(el=>el.clientWidth)>initial,'inspector resize grows width');await save();
  check((await cfg()).control_center.workspace.inspector_width>300,'inspector width persists');
  await page.click('[data-page="models"]');await page.waitForSelector('#modelsApps .app-chip');
  check(await page.locator('#modelsApps .app-chip').count()===3,'model page lists direct, opencode and hermes applications');
  check((await page.locator('#universeCount').textContent()).includes('снимок'),'the page names the catalog snapshot it shows');
  check(await page.locator('#modelsRows .model-card').count()>0,'default view lists usable and free models');
  check(await page.evaluate(()=>[...document.querySelectorAll('#modelsRows .model-card')].every(c=>/ГОТОВА|FREE/.test(c.textContent))),'default view never shows a model that is neither ready nor free');
  await page.check('#modelFree');await page.waitForFunction(()=>[...document.querySelectorAll('#modelsRows .model-card')].every(c=>c.textContent.includes('FREE')));
  check(await page.locator('#modelsRows .model-card').count()>0,'FREE switch keeps free models');await page.uncheck('#modelFree');await page.waitForTimeout(200);
  await page.fill('#modelQuery','zzz-нет-такой-модели');await page.waitForFunction(()=>document.querySelectorAll('#modelsRows .model-card').length===0);
  check(await page.locator('#modelsRows .model-card').count()===0,'smart search filters the list');
  await page.fill('#modelQuery','');await page.waitForFunction(()=>document.querySelectorAll('#modelsRows .model-card').length>0);
  await page.click('[data-provider-row="opencode"]');await page.waitForFunction(()=>document.querySelectorAll('#modelsRows .model-card').length>0);
  check(await page.evaluate(()=>[...document.querySelectorAll('#modelsRows .model-card')].every(c=>c.dataset.model.startsWith('opencode/'))),'provider rail filters the list to that provider');
  await page.click('[data-provider-row="opencode"]');await page.waitForTimeout(200);
  await page.check('#modelAll');await page.waitForSelector('#modelAllReset:not([hidden])');
  await page.waitForFunction(()=>modelSpace.catalog!==null,undefined,{timeout:30000});
  check(await page.evaluate(()=>document.querySelectorAll('#modelsRows .model-card').length>=modelSpace.snapshot.models.length),'all-models mode adds the rest of the catalog');
  await page.click('#modelAllReset');await page.waitForFunction(()=>document.querySelector('#modelAllReset').hidden&&!document.querySelector('#modelAll').checked);
  check(await page.evaluate(()=>document.querySelector('#modelAll').checked===false),'reset button returns to the usable list');
  await page.click('[data-page="pipelines"]');await page.click('[data-tab="models"]');await page.waitForSelector('#solverModels [data-params="0"]');
  await page.click('#solverModels [data-params="0"]');await page.waitForSelector('#mpReasoning');await page.selectOption('#mpReasoning','high');await page.click('#mpSave');
  await page.waitForFunction(()=>!document.querySelector('#dialog').open&&!state.dirty);
  c=await cfg();check(c.pipelines[0].nodes.find(n=>n.model_pool).model_pool.models[0].app_params?.reasoning==='high','app-owned model parameter survives the save round trip');
  const mock={id:'ux-run',running:false,duration_ms:3000,trace:{pipeline_id:c.active_pipeline_id,pipeline_name:'Saved run',config_revision:2,budget:{max_model_calls:6},winner:{id:'b',provider_id:'opencode',model_id:'B'},events:[{kind:'attempt_start',at_ms:0,node_id:'models',attempt:{id:'a',provider_id:'opencode',model_id:'A',credential_alias:'Alias A'}},{kind:'attempt_start',at_ms:2000,node_id:'models',attempt:{id:'b',provider_id:'opencode',model_id:'B',credential_alias:'Alias B'}},{kind:'attempt_end',at_ms:3000,node_id:'models',outcome:'success',duration_ms:1000,attempt:{id:'b',provider_id:'opencode',model_id:'B',credential_alias:'Alias B'}},{kind:'winner',at_ms:3000,node_id:'models',attempt:{id:'b',provider_id:'opencode',model_id:'B'}},{kind:'attempt_end',at_ms:3000,node_id:'models',outcome:'cancelled',duration_ms:3000,attempt:{id:'a',provider_id:'opencode',model_id:'A',credential_alias:'Alias A'}}]}};
  await page.evaluate(job=>{window.__fixtureJob=job;state.job=job;state.page='runs';renderPage();},mock);
  check((await page.locator('#liveRun').textContent()).includes('Остановлен после победы другой модели'),'race losers distinguished from user cancellation');
  check(await page.locator('.execution-timeline .timeline-row').count()===2,'each network attempt gets timeline row');
  check(await page.locator('#liveRun .run-summary').evaluate(el=>getComputedStyle(el).display)==='block','run explanation is stacked, not cramped into one flex row');
  await page.click('#liveRun .full-trace>summary');await page.waitForTimeout(1700);check(await page.locator('#liveRun .full-trace').evaluate(el=>el.open),'polling preserves expanded journal');
  await page.screenshot({path:path.join(artifacts,'run-timeline-1024.png')});
  await page.locator('#liveRun [data-insight-action="budget"]').click();await page.waitForSelector('#bCalls');check(await page.locator('#bCalls').count()===1,'run summary links to budget settings');
  await page.evaluate(()=>{window.__fixtureJob={...window.__fixtureJob,running:true};state.job=window.__fixtureJob;updateApplyState();});check(await page.locator('#saveConfig').isDisabled(),'apply blocked with running reason visible');
  check((await page.locator('#currentExecution').textContent()).includes('r2'),'current run snapshot revision stays visible');
  await page.evaluate(()=>{window.__fixtureJob=null;state.job={};updateApplyState();});
  for(const viewport of [{width:1024,height:768},{width:820,height:614}]){
   await page.setViewportSize(viewport);await page.click('[data-page="pipelines"]');await page.waitForSelector('#canvas');
   for(const selector of ['#saveConfig','#canvas','#inspector','#findSetting','#helpToggle']){if(await page.locator(selector).isHidden())continue;const b=await page.locator(selector).boundingBox();check(b&&b.x>=0&&b.y>=0&&b.x+b.width<=viewport.width+1&&b.y+b.height<=viewport.height+1,'layout fits '+selector+' at '+viewport.width);}
   await page.screenshot({path:path.join(artifacts,'workspace-'+viewport.width+'.png')});
  }
  await page.click('#aboutStudio');await page.waitForSelector('.about-grid');check((await page.locator('.about-grid').textContent()).includes('Win32'),'technical details available in About');
  check(errors.length===0,'no JavaScript errors: '+errors.join(';'));
  console.log(JSON.stringify({passed:count,js_errors:errors,live_model_requests:0,network_probes:0,consent_buttons:'stubbed',mode:'Chromium UX + real Go handlers',screenshots:artifacts}));
 }catch(e){if(page)await page.screenshot({path:path.join(artifacts,'failure.png')}).catch(()=>{});throw e;}finally{if(browser)await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
