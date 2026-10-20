"""Scope-owned cgroup/process metadata; never read argv or environments."""
import json
import os
from pathlib import Path
import time


def snapshot():
    base = Path('/sys/fs/cgroup')
    cgroup = {n: (base / n).read_text() if (base / n).exists() else 'UNAVAILABLE' for n in
              ('cpu.stat', 'cpu.max', 'memory.stat', 'memory.peak', 'memory.events.local', 'memory.current', 'memory.events', 'memory.max', 'memory.swap.current')}
    processes = []
    for entry in Path('/proc').iterdir():
        if not entry.name.isdecimal():
            continue
        try:
            data = dict(line.split(':', 1) for line in (entry / 'status').read_text().splitlines() if ':' in line)
            processes.append({'pid': int(entry.name), **{k: data[k].strip() for k in ('VmRSS', 'VmHWM', 'Threads') if k in data}})
        except OSError:
            pass
    return {'monotonic_ns': time.monotonic_ns(), 'cgroup': cgroup, 'process_memory_no_argv_or_environment': processes}


if __name__ == '__main__':
    import sys
    evidence = Path(os.environ['UI188_EVIDENCE_DIRECTORY'])
    with (evidence / 'resource-snapshots.jsonl').open('a') as log:
        log.write(json.dumps({'checkpoint': sys.argv[1], **snapshot()}) + '\n')
