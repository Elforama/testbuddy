#!/usr/bin/env python3
"""AND-005: rapidly stop/rebind the editor service after an idle blocking accept.

No app input is sent. The readiness probe deliberately names an impossible field.
The short endpoint retry is test setup only, not runner/UI polling.
"""
import argparse,json,socket,subprocess,tempfile,time
from pathlib import Path

p=argparse.ArgumentParser();p.add_argument('--tb',required=True);p.add_argument('--adb',required=True);p.add_argument('--device',default='emulator-5554');p.add_argument('--rounds',type=int,default=20);p.add_argument('--output',required=True);a=p.parse_args()
tb=str(Path(a.tb).resolve());adb=[a.adb,'-s',a.device];out=Path(a.output).resolve();out.mkdir(parents=True,exist_ok=True);rows=[]
try:
 with tempfile.TemporaryDirectory(prefix='tb-editor-lifecycle-') as temp:
  flow=Path(temp)/'lifecycle.yaml';flow.write_text('appId: dev.speedrun.bench\n---\n- evalScript: output.editorLifecycle = true\n')
  for n in range(1,a.rounds+1):
   with (out/f'round-{n:02}.log').open('w') as log:
    subprocess.run([tb,'test',str(flow),'-p','android','--device',a.device,'--retries','0','--keep-agents'],stdout=log,stderr=subprocess.STDOUT,check=True,timeout=45)
   port=subprocess.check_output(adb+['forward','tcp:0','localabstract:testbuddy-editor'],text=True).strip()
   try:
    deadline=time.monotonic()+3;response=''
    while time.monotonic()<deadline:
     try:
      with socket.create_connection(('127.0.0.1',int(port)),timeout=1) as s:
       s.sendall((json.dumps(dict(pkg='invalid-lifecycle-probe',window=-1,node=-1,id='',cls=''))+'\n').encode());response=s.makefile('rb').readline().decode().strip()
      if response=='rejected':break
     except (OSError,TimeoutError):pass
     time.sleep(.05)
    assert response=='rejected',f'round {n}: service did not bind its editor endpoint'
   finally:subprocess.run(adb+['forward','--remove','tcp:'+port],check=True,stdout=subprocess.DEVNULL)
   subprocess.run([tb,'release','-d',a.device],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.STDOUT,timeout=15)
   rows.append(dict(round=n,status='passed'));(out/'results.json').write_text(json.dumps(rows,indent=2)+'\n');print(n,'passed',flush=True)
finally:subprocess.run([tb,'release','-d',a.device],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
