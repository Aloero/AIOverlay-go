'use strict';
const {chromium}=require('playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),os=require('node:os');
const base=process.env.TEST_SERVER;if(!base)throw Error('Run with the isolated test runner: npm run test:all');
const artifacts=process.env.ARTIFACT_DIR||path.join(os.tmpdir(),'aio-studio-visual');fs.mkdirSync(artifacts,{recursive:true});
const calls=[],errors=[];let count=0,browser,page;
function ok(value,message){assert.ok(value,message);count++;}
async function readConfig(){const r=await fetch(base+'/api/config',{headers:{Authorization:'Bearer '+process.env.TEST_TOKEN}});if(!r.ok)throw Error('config failed');return r.json();}
(async()=>{
 try {
  browser=await chromium.launch({headless:true});
  page=await browser.newPage({viewport:{width:1024,height:768},deviceScaleFactor:1});
  page.on('pageerror',e=>errors.push(e.message));page.on('dialog',d=>d.accept());
  // Simulate the native binding, retaining real application handlers. No model
  // execution or capture is permitted; native Windows itself is not exercised.
  await page.exposeFunction('__testDispatch',async r=>{
   calls.push({path:r.path,method:r.method});
   if((r.path==='/api/job'&&r.method==='POST')||r.path.includes('/benchmark')||r.path.includes('/test/run')||r.path==='/api/providers/test'||r.path==='/api/vpn/probe')throw Error('TEST FORBIDS MODEL REQUESTS');
   if(r.path==='/desktop/close')return {};
   if(r.path==='/api/capture')return {image_base64:'dGVzdA==',bytes:4,duration_ms:1,method:'Synthetic fixture'};
   if(!r.path.startsWith('/api/'))throw Error('Unexpected test route');
   const reply=await fetch(base+r.path,{method:r.method,headers:{'Content-Type':'application/json',Authorization:'Bearer '+process.env.TEST_TOKEN},body:r.body===null||r.body===undefined?undefined:JSON.stringify(r.body)});
   if(!reply.ok)throw Error(await reply.text());return reply.json();
  });
  await page.addInitScript(()=>{window.studioDispatch=r=>window.__testDispatch(r).then(data=>window.__studioReply(r.id,{ok:true,data}),e=>window.__studioReply(r.id,{ok:false,error:e.message}));});
  await page.goto(base);await page.waitForSelector('#solverModels');
  ok(await page.locator('#runScreen').count()===0,'ordinary Run button removed');
  await page.click('[data-page="settings"]');await page.waitForSelector('#vpnEnabled');ok(await page.locator('#buildExe').count()===0,'build identity removed from runtime Studio');ok(await page.locator('#vpnConfig').count()===1,'AI VPN editor present');await page.click('[data-page="pipelines"]');await page.waitForSelector('#solverModels');
  await page.click('[data-page="runs"]');await page.waitForSelector('#benchmarkRuns');
  ok(await page.locator('#benchmarkRuns').inputValue()==='3','batch benchmark defaults to three runs');
  await page.evaluate(()=>{state.job={id:'fixture-benchmark',running:false,benchmark:{requested:3,total:2,median_ms:150,successful:2,success_rate:1,correct:1,unverified:1,cancelled:true,runs:[{run:1,duration_ms:100,verdict:'correct',text:'Q4 210',winner:{provider_id:'fake',model_id:'fixture'},ttft_ms:20,tps:5},{run:2,duration_ms:200,verdict:'unverified',text:'explanation',winner:null,ttft_ms:0,tps:0}]}};renderBenchmarkResult();});
  ok((await page.locator('#benchmarkResult').textContent()).includes('0.15'),'benchmark renders correct median');
  ok((await page.locator('#benchmarkResult').textContent()).includes('Проверить вручную'),'benchmark does not claim prose grading');
  await page.click('[data-page="settings"]');await page.waitForSelector('#runtimeDiagnostics');
  await page.locator('#hotkey_control').focus();await page.waitForTimeout(80);
  ok(calls.some(r=>r.path==='/api/hotkeys/editor'),'hotkey editor suppression reaches core');
  await page.click('[data-page="pipelines"]');await page.waitForSelector('#solverModels');
  ok(await page.locator('#studioRecovery').count()===0,'sidebar hotkey clutter removed');
  ok(!(await page.locator('.sidebar').textContent()).includes('JSON с ключами'),'sidebar secret banner removed');
  ok(!(await page.locator('#graphNodes').textContent()).includes('Основной монитор'),'source card clutter removed');
  ok(!(await page.locator('#graphLinks').textContent()).includes('success'),'normal edges need no success label');
  await page.selectOption('#sourceMode','catalog');
  while(await page.locator('#solverModels [data-remove]').count())await page.locator('#solverModels [data-remove]').first().click();
  await page.waitForSelector('#allowUnscored');await page.click('#allowUnscored');
  await page.waitForFunction(()=>document.querySelector('#poolPlan .plan-heading strong')?.textContent!=='0 моделей');
  await page.click('#saveConfig');await page.waitForFunction(()=>!document.querySelector('#saveState').classList.contains('dirty'));
  let cfg=await readConfig(),pool=cfg.pipelines.find(p=>p.id===cfg.active_pipeline_id).nodes.find(n=>n.model_pool).model_pool;
  ok(pool.models.length===0&&pool.selection.mode==='catalog','catalog-only empty manual list saves');
  ok(pool.selection.free_only&&pool.quality_floor===0,'explicit FREE/unscored policy saved');
  await page.selectOption('#poolPolicy','parallel_race');await page.fill('#dispatchParallel','8');await page.locator('#dispatchParallel').press('Tab');
  ok((await page.locator('.dispatch-effective').textContent()).includes('до 3 одновременно'),'global concurrency cap is visible');
  await page.selectOption('#poolPolicy','staggered_race');await page.fill('#dispatchHedge','2000');await page.locator('#dispatchHedge').press('Tab');
  await page.waitForSelector('#showPoolPlan');await page.click('#showPoolPlan');await page.waitForSelector('#queueRows tr');
  ok(await page.locator('#queueRows tr').count()>0,'catalog plan shows model queue');
  ok((await page.locator('#dialogBody').textContent()).includes('вызовов'),'queue shows call budget');await page.click('#closeDialog');
  const canvas=await page.locator('#canvas').boundingBox();
  const before=await page.locator('#canvasInner').evaluate(e=>e.style.transform);
  await page.mouse.move(canvas.x+18,canvas.y+18);await page.mouse.down();await page.mouse.move(canvas.x+75,canvas.y+65,{steps:5});await page.mouse.up();
  ok(await page.locator('#canvasInner').evaluate(e=>e.style.transform)!==before,'left drag pans blank canvas');
  await page.click('#fitGraph');
  await page.locator('#previewPlan').hover();await page.keyboard.down('Alt');await page.waitForSelector('#contextHelp:not(.hidden)');
  ok((await page.locator('#contextHelp').textContent()).includes('не вызывает AI'),'Alt hover shows useful explanation');
  await page.keyboard.up('Alt');ok(await page.locator('#contextHelp').evaluate(e=>e.classList.contains('hidden')),'Alt release hides help');
  await page.click('#saveConfig');await page.waitForFunction(()=>!document.querySelector('#saveState').classList.contains('dirty'));
  cfg=await readConfig();ok(!!cfg.control_center.layout?.[cfg.active_pipeline_id],'canvas layout persists in portable config');
  // Control rectangles must stay inside the work area at the minimum target size.
  for(const selector of ['#inspector','#canvas','#saveConfig','#helpToggle']) {
   const r=await page.locator(selector).boundingBox();ok(r&&r.x>=0&&r.y>=0&&r.x+r.width<=1025&&r.y+r.height<=769,'1024x768 fits '+selector);
  }
  await page.screenshot({path:path.join(artifacts,'pipeline-1024x768.png')});
  const next=cfg.pipelines.find(p=>p.id!==cfg.active_pipeline_id).id;
  await page.click('[data-pipeline="'+next+'"]');await page.waitForFunction(id=>state.config.active_pipeline_id===id,next);
  ok((await readConfig()).active_pipeline_id===next,'sidebar selection persists active pipeline without inference');
  await page.click('[data-page="providers"]');await page.waitForSelector('[data-connect-provider="anthropic"]');
  await page.fill('#providerSearch','Anthropic');await page.click('[data-connect-provider="anthropic"]');await page.waitForSelector('#bulkKeys');
  ok(await page.locator('#ocProviderID').count()===0,'provider ID does not require manual entry');
  await page.fill('#bulkKeys','FAKE-ALPHA-TEST\nFAKE-BETA-TEST\\nFAKE-ALPHA-TEST');await page.click('#addKeys');
  ok(await page.locator('#providerKeys .key-row').count()===2,'provider key batch deduplicated');
  ok(!(await page.locator('#providerKeys').textContent()).includes('FAKE-ALPHA'),'saved key is masked');
  ok(await page.locator('#bulkKeys').inputValue()==='','paste field cleared');
  ok((await page.locator('.secret-notice').textContent()).includes('открытом виде'),'secret warning stays beside credentials');
  await page.screenshot({path:path.join(artifacts,'connection-1024x768.png')});
  await page.click('#providerDone');await page.waitForFunction(()=>!document.querySelector('#dialog').open);
  cfg=await readConfig();const connection=cfg.providers.find(p=>p.opencode_provider_id==='anthropic');
  ok(connection?.kind==='embedded_opencode'&&connection.credential_env.includes('ANTHROPIC_API_KEY'),'catalog configures correct OpenCode identity/env');
  ok(connection.credential_pool.keys.length===2,'provider-only save persisted keys');
  await page.waitForSelector('#providerSearch');await page.fill('#providerSearch','');
  await page.screenshot({path:path.join(artifacts,'providers-1024x768.png')});
  ok(await page.locator('#providerDirectory td').first().evaluate(e=>getComputedStyle(e).borderBottomStyle)==='solid','directory separators resolve theme colors');
  // The chosen directory provider must not be mixed with similarly named ones.
  await page.fill('#providerSearch','Anthropic');await page.click('[data-models-provider="anthropic"]');await page.waitForSelector('#catalogRows [data-select-model]');
  const refs=await page.locator('#catalogRows .ref').allTextContents();ok(refs.every(r=>r.startsWith('anthropic/')),'directory opens exact provider catalog');
  ok(await page.locator('#catalogRows [data-route="direct"]').count()===0,'OpenCode directory retains explicit transport');
  await page.locator('#catalogRows [data-route="opencode"]').first().click();await page.waitForFunction(()=>!document.querySelector('#dialog').open);
  ok((await page.locator('#solverModels').textContent()).includes('Anthropic'),'model added to selected pool through configured provider');
  await page.click('#saveConfig');await page.waitForFunction(()=>!document.querySelector('#saveState').classList.contains('dirty'));
  await page.click('#newPipeline');await page.fill('#newPipelineName','Catalog fixture');await page.click('#createPipeline');await page.waitForFunction(()=>state.config.pipelines.find(p=>p.id===state.config.active_pipeline_id)?.name==='Catalog fixture');
  cfg=await readConfig();ok(cfg.pipelines.find(p=>p.id===cfg.active_pipeline_id)?.name==='Catalog fixture','new catalog-only pipeline becomes active');
  await page.setViewportSize({width:1320,height:880});await page.click('#fitGraph');await page.screenshot({path:path.join(artifacts,'pipeline-1320x880.png')});
  await page.setViewportSize({width:820,height:614});await page.click('#fitGraph');
  for(const selector of ['#inspector','#canvas','#saveConfig','#helpToggle']) {
   const r=await page.locator(selector).boundingBox();ok(r&&r.width>0&&r.x>=0&&r.y>=0&&r.x+r.width<=821&&r.y+r.height<=615,'compact logical viewport fits '+selector);
  }
  await page.screenshot({path:path.join(artifacts,'pipeline-820x614.png')});
  await page.setViewportSize({width:1024,height:768});await page.click('#fitGraph');
  await page.evaluate(()=>{
   const make=(id,model,key)=>({id,provider_id:'opencode',model_id:model,credential_id:key,credential_alias:key,transport:'Via OpenCode'});
   state.job={id:'test-only',pipeline_id:state.pipeline,running:true,duration_ms:2500,trace:{events:[{kind:'attempt_start',at_ms:0,node_id:'solver',attempt:make('a','model-a','Alias A')},{kind:'attempt_start',at_ms:2000,node_id:'solver',attempt:make('b','model-b','Alias B')}],pipeline_id:state.pipeline}};
   renderAttemptState();
  });
  ok(await page.locator('.live-attempt').count()===2,'simultaneous requests are individually visible');
  ok((await page.locator('#attemptState').textContent()).includes('Alias B'),'live attempt shows credential alias');
  ok(calls.filter(r=>r.path==='/api/job'&&r.method==='POST').length===0,'settings UI never sent an inference request');
  ok(errors.length===0,'no JavaScript errors: '+errors.join('; '));
  console.log(JSON.stringify({passed:count,js_errors:errors,live_model_requests:0,mode:'Chromium render + native-binding simulation + real Go handlers',screenshots:artifacts}));
 }catch(e){if(page)await page.screenshot({path:path.join(artifacts,"failure.png")}).catch(()=>{});throw e;}finally{if(browser)await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
