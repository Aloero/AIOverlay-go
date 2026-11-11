'use strict';
// Measure S5e: /api/universe latency and proof that no model runtime child is spawned
// for a config that only registers opencode+direct providers (hermes absent from PATH).
const fs=require('node:fs'),os=require('node:os'),path=require('node:path'),net=require('node:net');
const {spawn,spawnSync}=require('node:child_process');
const root=path.resolve(__dirname,'../..');
const temp=fs.mkdtempSync(path.join(os.tmpdir(),'aio-universe-'));
const fixture=JSON.parse(fs.readFileSync(path.join(root,'examples/config.example.json'),'utf8'));
const token=require('node:crypto').randomBytes(32).toString('hex');
async function port(){return new Promise((resolve,reject)=>{const s=net.createServer();s.on('error',reject);s.listen(0,'127.0.0.1',()=>{const n=s.address().port;s.close(()=>resolve(n));});});}
(async()=>{
 let server;
 try{
  for(const p of fixture.providers)p.credential_pool.keys=[];
  fixture.control_center.listen_address='127.0.0.1:'+await port();
  const cfg=path.join(temp,'config.json'),binary=path.join(temp,'aio-test');
  fs.writeFileSync(cfg,JSON.stringify(fixture,null,2));
  spawnSync('go',['build','-o',binary,'./cmd/aioverlay'],{cwd:root,stdio:'inherit',timeout:180000});
  server=spawn(binary,['--headless','--headless-token',token,'--config',cfg],{cwd:root,stdio:['ignore','pipe','pipe']});
  const base='http://'+fixture.control_center.listen_address;
  for(let i=0;i<100;i++){try{if((await fetch(base+'/api/config',{headers:{Authorization:'Bearer '+token}})).ok)break;}catch{}await new Promise(r=>setTimeout(r,50));}
  const kids=()=>spawnSync('ps',['-o','pid=,comm=','--ppid',String(server.pid)],{encoding:'utf8'}).stdout.trim();
  const times=[];
  for(let i=0;i<5;i++){const t=process.hrtime.bigint();const r=await fetch(base+'/api/universe',{headers:{Authorization:'Bearer '+token}});const u=await r.json();times.push(Number(process.hrtime.bigint()-t)/1e6);if(i===4)console.log(JSON.stringify({apps:u.apps.map(a=>({id:a.id,available:a.available,version:a.version||null})),providers:u.providers.length,models:u.models.length}));}
  console.log('children after 5x /api/universe:',kids()||'(none)');
  console.log('latency_ms',times.map(t=>t.toFixed(1)).join(', '));
 }finally{if(server)server.kill('SIGTERM');fs.rmSync(temp,{recursive:true,force:true});}
})().catch(e=>{console.error(e);process.exitCode=1;});
