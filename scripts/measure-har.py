#!/usr/bin/env python3
"""Recalculate Week 4 measurements from original Firefox HAR recordings."""
import argparse
import base64
import json
from datetime import datetime, timedelta
from pathlib import Path
from urllib.parse import urlsplit


def timestamp(value):
    return datetime.fromisoformat(value.replace('Z', '+00:00'))


def body(entry):
    content = entry['response'].get('content', {})
    text = content.get('text', '')
    if content.get('encoding') == 'base64':
        text = base64.b64decode(text).decode('utf-8')
    return json.loads(text)


def measure(path, label):
    entries = json.loads(path.read_text())['log']['entries']
    posts = [e for e in entries if e['request']['method'] == 'POST'
             and urlsplit(e['request']['url']).path == '/v1/images'
             and e['response']['status'] == 202]
    if len(posts) != 1:
        raise ValueError(f'{path.name}: expected exactly one accepted upload')
    post = posts[0]
    job_id = body(post)['job_id']
    gets = sorted([e for e in entries if e['request']['method'] == 'GET'
                   and urlsplit(e['request']['url']).path == f'/v1/jobs/{job_id}'],
                  key=lambda e: timestamp(e['startedDateTime']))
    completed = []
    requests = []
    for entry in gets:
        try:
            response = body(entry)
        except (ValueError, UnicodeDecodeError):
            response = {}
        requests.append(dict(started_at=entry['startedDateTime'],
                             duration_ms=entry['time'],
                             http_status=entry['response']['status'],
                             job_status=response.get('status')))
        if entry['response']['status'] == 200 and response.get('status') == 'completed':
            completed.append((entry, response))
    if not completed:
        raise ValueError(f'{path.name}: no successful completed status response')
    entry, response = min(completed, key=lambda pair:
        timestamp(pair[0]['startedDateTime']) + timedelta(milliseconds=pair[0]['time']))
    queued, started, finished = (timestamp(response[k]) for k in
                                ('queued_at', 'started_at', 'completed_at'))
    observed = timestamp(entry['startedDateTime']) + timedelta(milliseconds=entry['time'])
    ms = lambda delta: round(delta.total_seconds() * 1000, 2)
    return dict(run=label, source=path.name, job_id=job_id,
                post_started=post['startedDateTime'],
                queued_at=response['queued_at'], started_at=response['started_at'],
                completed_at=response['completed_at'], ack_ms=post['time'],
                queue_ms=ms(started-queued), processing_ms=ms(finished-started),
                job_ms=ms(finished-queued), polls=len(gets),
                detection_ms=ms(observed-finished), status_requests=requests)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('har_directory', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--compare', type=Path)
    args = parser.parse_args()
    sources = [('single-image.har', 'Single')] + [(f'{i}.har', f'Burst {i}') for i in range(1, 6)]
    results = [measure(args.har_directory / name, label) for name, label in sources]
    args.output.write_text(json.dumps(results, indent=2) + '\n')
    print('Run | Ack ms | Queue ms | Processing ms | Job ms | GETs | Detection ms')
    for row in results:
        print(' | '.join(str(row[k]) for k in
              ('run', 'ack_ms', 'queue_ms', 'processing_ms', 'job_ms', 'polls', 'detection_ms')))
    if args.compare:
        previous = json.loads(args.compare.read_text())
        if results != previous:
            raise SystemExit('MISMATCH: regenerated evidence differs from saved evidence')
        print('PASS: all regenerated records exactly match the saved evidence.')


if __name__ == '__main__':
    main()
