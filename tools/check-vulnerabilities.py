#!/usr/bin/env python3
"""Fail on symbol-level findings in a govulncheck JSON event stream."""
import json,sys
text=open(sys.argv[1]).read();decoder=json.JSONDecoder();findings=set();config=False;progress=False
while text.strip():
    obj,end=decoder.raw_decode(text.lstrip());text=text.lstrip()[end:]
    config |= 'config' in obj
    progress |= 'progress' in obj
    if 'finding' in obj:
        f=obj['finding']
        if any('function' in item for item in f.get('trace',[])):findings.add(f['osv'])
if not config or not progress:sys.exit('Incomplete vulnerability scan stream')
if findings:sys.exit('Reachable advisories: '+', '.join(sorted(findings)))
print('No symbol-level advisory findings')
