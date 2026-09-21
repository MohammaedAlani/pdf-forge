#!/usr/bin/env python3
"""Exercise rendering and PDF tools against a disposable running PDF Forge server."""
import argparse
import base64
import json
import struct
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--url', default='http://127.0.0.1:18090')
args = parser.parse_args()


def post(path, payload, binary=False):
    request = urllib.request.Request(args.url + path, json.dumps(payload).encode(),
                                     {'Content-Type': 'application/json'})
    with urllib.request.urlopen(request, timeout=60) as response:
        data = response.read()
    if binary:
        assert data.startswith(b'%PDF-'), path
        return data
    result = json.loads(data)
    assert result.get('success', True), result
    return result


def b64(data):
    return base64.b64encode(data).decode()


html = '<html><body><h1>First page</h1><div style="break-before:page"><h1>Second page</h1></div></body></html>'
pdf = post('/html', {'html': html}, True)
assert post('/manipulate', {'operation': 'info', 'pdf': b64(pdf)})['info']['page_count'] == 2
split = post('/manipulate', {'operation': 'split', 'pdf': b64(pdf)})
assert split['count'] == 2
merged = post('/merge', {'pdfs': split['files']}, True)
assert post('/manipulate', {'operation': 'info', 'pdf': b64(merged)})['info']['page_count'] == 2
compressed = post('/manipulate', {'operation': 'compress', 'pdf': b64(pdf)})
assert base64.b64decode(compressed['pdf']).startswith(b'%PDF-')
images = post('/manipulate', {'operation': 'to_images', 'pdf': b64(pdf),
                             'options': {'image_format': 'png', 'dpi': 72}})
assert images['count'] == 2
for encoded in images['files']:
    image = base64.b64decode(encoded)
    assert image.startswith(b'\x89PNG\r\n\x1a\n')
    width, height = struct.unpack('>II', image[16:24])
    assert 600 <= max(width, height) <= 850, (width, height)
post('/convert', {'html': html, 'options': {'watermark': {'text': 'DRAFT'},
                                          'metadata': {'title': 'Smoke test'},
                                          'security': {'user_password': 'smoke', 'owner_password': 'owner',
                                                       'encryption_bits': 256}}}, True)
batch = post('/batch', {'requests': [{'html': '<body>A</body>'}, {'html': '<body>B</body>'}], 'merge': True})
assert batch['completed'] == 2 and batch['failed'] == 0
assert base64.b64decode(batch['merged_pdf']).startswith(b'%PDF-')
print('PASS: rendering, page count, split, merge, compression, rasterization/DPI, watermark, metadata, encryption, batch merge')
