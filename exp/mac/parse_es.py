#!/usr/bin/env python3

import json
from datetime import datetime
while True:
    try:
        line = input()
    except EOFError:
        break
    ev = json.loads(line)
    #ts = datetime.fromisoformat(ev['time'])
    process = ev['process']['signing_id']
#    if ev['process']['signing_id'] != 'com.apple.Virtualization.VirtualMachine':
#    if ev['process']['signing_id'] != 'com.apple.login':
#        continue
    #time_str = ts.strftime('%H:%M:%S')
    type = list(ev['event'].keys())[0]
    data = ev['event'][type]
    hdr = f'{process}\t{type}'
    desc = ''
    if 'target' in data and data['target']:
        target = data["target"]
        if 'stat' in target:
            del target['stat']
        if 'path' in target:
            desc += f'\t{target["path"]}'
            del target['path']
        if 'path_truncated' in target:
            if target['path_truncated']:
                desc += ' (truncated)'
            del target['path_truncated']
        if target:
            desc += f'\t{json.dumps(target)}'
        del data['target']
    desc += f'\t{json.dumps(data)}'
    print(hdr + desc)
