'use strict';
// Build and run the real control handlers against a disposable configuration.
// No user config is written and no AI provider request is allowed by the suites.
const fs=require('node:fs'),os=require('node:os'),path=require('node:path'),net=require('node:net');
const {spawn,spawnSync}=require('node:child_process');
const root=path.resolve(__dirname,'../..'),temp=fs.mkdtempSync(path.join(os.tmpdir(),'aio-studio-tests-'));
const fixture=JSON.parse(fs.readFileSync(path.join(root,'examples/config.example.json'),'utf8'));
const go=process.env.GO||'go';let server;
async function port(){return new Promise((resolve,reject)=>{const s=net.createServer();s.on('error',reject);s.listen(0,'127.0.0.1',()=>{const n=s.address().port;s.close(()=>resolve(n));});});}
async function waitReady(base){for(let i=0;i<100;i++){try{if((await fetch(base+'/api/config')).ok)return;}catch{}await new Promise(r=>setTimeout(r,50));}throw Error('test server did not start');}
(async()=>{
 try {
  for(const p of fixture.providers) p.credential_pool.keys=[];
  fixture.control_center.listen_address='127.0.0.1:'+await port();
  const cfg=path.join(temp,'config.json'),binary=path.join(temp,process.platform==='win32'?'aio-test.exe':'aio-test');
  fs.writeFileSync(cfg,JSON.stringify(fixture,null,2));
  let result=spawnSync(go,['build','-o',binary,'./cmd/aioverlay'],{cwd:root,stdio:'inherit',timeout:120000});
  if(result.error||result.status!==0)throw result.error||Error('Go test host build failed');
  server=spawn(binary,['--headless','--config',cfg],{cwd:root,stdio:['ignore','pipe','pipe']});
  let logs='';server.stdout.on('data',v=>logs+=v);server.stderr.on('data',v=>logs+=v);
  const base='http://'+fixture.control_center.listen_address;await waitReady(base);
  for(const suite of process.argv.includes('--dom-only')?['smoke.cjs']:['smoke.cjs','studio-features.cjs']) {
   const clean=structuredClone(fixture);
   const reset=await fetch(base+'/api/config',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify(clean)});
   if(!reset.ok)throw Error('Could not reset isolated UI fixture: '+await reset.text());
   result=spawnSync(process.execPath,[path.join(__dirname,suite)],{cwd:root,env:{...process.env,TEST_SERVER:base},stdio:'inherit',timeout:120000});
   if(result.error||result.status!==0)throw result.error||Error(suite+' failed');
  }
 } finally {
  if(server){server.kill('SIGTERM');await Promise.race([new Promise(r=>server.once('exit',r)),new Promise(r=>setTimeout(r,2000))]);}
  fs.rmSync(temp,{recursive:true,force:true});
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
