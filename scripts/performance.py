#!/usr/bin/env python3
"""Small closed-loop HTML baseline; run only against an authorized test server."""
import argparse
import concurrent.futures
import json
import os
import platform
import statistics
import time
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--url', default='http://127.0.0.1:18089')
parser.add_argument('--output')
args = parser.parse_args()
body = json.dumps({'html': '<!doctype html><html><body><h1>Performance assessment</h1><p>Local single-page document without external assets.</p></body></html>'}).encode()


def render(_):
    start = time.perf_counter()
    headers = {'Content-Type': 'application/json'}
    if os.environ.get('API_KEY'):
        headers['X-API-Key'] = os.environ['API_KEY']
    try:
        request = urllib.request.Request(args.url.rstrip('/') + '/html', data=body, headers=headers)
        with urllib.request.urlopen(request, timeout=90) as response:
            data = response.read()
            return {'seconds': time.perf_counter() - start,
                    'ok': response.status == 200 and data.startswith(b'%PDF-')}
    except Exception as exc:
        return {'seconds': time.perf_counter() - start, 'ok': False, 'error': str(exc)}


report = {'platform': platform.platform(), 'logical_cpus': os.cpu_count(),
          'workload': 'single-page inline HTML, no external resources; no request warm-up', 'runs': []}
for concurrency, count in [(1, 4), (4, 8), (8, 8)]:
    start = time.perf_counter()
    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
        results = list(pool.map(render, range(count)))
    elapsed = time.perf_counter() - start
    times = [x['seconds'] for x in results]
    successful = sum(x['ok'] for x in results)
    row = {'concurrency': concurrency, 'requests': count, 'successful': successful,
           'elapsed_seconds': round(elapsed, 3),
           'requests_per_second': round(successful / elapsed, 3),
           'min_seconds': round(min(times), 3),
           'median_seconds': round(statistics.median(times), 3),
           'max_seconds': round(max(times), 3)}
    failures = [r['error'] for r in results if 'error' in r]
    if failures:
        row['errors'] = failures
    report['runs'].append(row)
    print(json.dumps(row), flush=True)
if args.output:
    with open(args.output, 'w') as target:
        json.dump(report, target, indent=2)
if any(row['successful'] != row['requests'] for row in report['runs']):
    raise SystemExit(1)
