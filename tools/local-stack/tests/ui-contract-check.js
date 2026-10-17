// Uses the actual v0.7.6 bundle, not a hand-written substitute decoder.
const fs=require('fs'),vm=require('vm'),assert=require('assert');
const [,,bundle,worker,base='http://127.0.0.1:8080']=process.argv;
const source=fs.readFileSync(bundle,'utf8');
function extract(name){
 const start=source.indexOf('function '+name+'(');assert(start>=0,name);
 const open=source.indexOf('{',start);let depth=0,end=open;
 // These two small minified contract parsers have no braces inside strings.
 for(;end<source.length;end++){if(source[end]==='{')depth++;else if(source[end]==='}'&&!--depth){end++;break;}}
 return source.slice(start,end);
}
const sandbox={isObject:x=>!!x&&typeof x==='object'&&!Array.isArray(x)};
vm.createContext(sandbox);
vm.runInContext(extract('parseFeaturedServersPayload')+';'+extract('parseIdentities'),sandbox);
(async()=>{
 for(const path of ['/api/identities','/api/ticket/identities']){
  const r=await fetch(base+path,{method:path.includes('/ticket/')?'POST':'GET'});assert.equal(r.status,200);
  sandbox.value=await r.json();assert.equal(vm.runInContext('parseIdentities(value).length',sandbox),0);
 }
 sandbox.value=await (await fetch(base+'/v1/public/featured-servers')).json();
 assert.equal(vm.runInContext('parseFeaturedServersPayload(value).pinnedServers.length',sandbox),0);
 for(const path of ['/v1/public/tweets','/v1/public/notices/fivem'])assert(Array.isArray(await(await fetch(base+path)).json()));
 let code=fs.readFileSync(worker,'utf8');assert(code.includes('new ServersWorker;'));
 code=code.replace('new ServersWorker;','globalThis.__decode=(bytes)=>$root.master.Server.decode(bytes);globalThis.__makeWorker=()=>new ServersWorker;');
 const localFetch=(input,options)=>{const u=new URL(typeof input==='string'?input:input.url);return fetch(base+u.pathname+u.search,options);};
 const context={fetch:localFetch,Request,Response,Headers,URL,performance,setInterval,clearInterval,console,Uint8Array,ArrayBuffer,DataView,TextDecoder,TextEncoder,setTimeout,clearTimeout,postMessage:()=>{}};
 vm.createContext(context);vm.runInContext(code,context);
 // Deliberate test-only heartbeat: never added to a user's real registry.
 const hb={port:30120,fallbackData:{players:[],info:{resources:['chat'],vars:{}},dynamic:{hostname:'TEST FIXTURE - NOT A LIVE GAME SERVER',clients:2,svMaxclients:48}}};
 assert.equal((await fetch(base+'/api/serverlist/ingress',{method:'POST',body:JSON.stringify(hb)})).status,200);
 const bytes=Buffer.from(await(await fetch(base+'/api/servers/streamRedir/')).arrayBuffer());
 let pos=0,count=0;
 while(pos<bytes.length){const len=bytes.readUInt32LE(pos);pos+=4;assert(pos+len<=bytes.length);
  const decoded=context.__decode(new Uint8Array(bytes.subarray(pos,pos+len)));pos+=len;count++;
  assert.equal(decoded.EndPoint,'127.0.0.1:30120');assert.equal(decoded.Data.clients,2);assert.equal(decoded.Data.vars.gamename,'gta5');assert.equal(decoded.Data.connectEndPoints[0],decoded.EndPoint);
 }
 assert.equal(count,1);
 // Run the real worker's fetch/filter/transform path as well as its decoder.
 const events=[];
 const complete=new Promise((resolve,reject)=>{
  const timeout=setTimeout(()=>reject(Error('worker timeout')),5000);
  context.postMessage=event=>{events.push(event);if(event.type==='allServersError'){clearTimeout(timeout);reject(Error(event.data));}if(event.type==='allServersEnd'){clearTimeout(timeout);resolve();}};
 });
 context.fetch=(input,options)=>{const u=new URL(typeof input==='string'?input:input.url);return fetch(base+u.pathname+u.search,options);};
 context.__makeWorker().init({gameNames:['gta5'],serversChunkSize:500});
 await complete;
 const servers=events.filter(e=>['allServersChunk','allServersEnd'].includes(e.type)).flatMap(e=>e.data);
 assert.equal(servers.length,1);assert.equal(servers[0].id,'127.0.0.1:30120');
 console.log('PASS: actual bundled worker fetched, decoded, filtered and emitted one test server to the UI message channel');
 console.log('PASS: actual v0.7.6 identity/featured parsers and protobuf worker decoder; HTTP heartbeat -> stream -> decoded gameplay endpoint');
})().catch(e=>{console.error(e);process.exit(1)});
