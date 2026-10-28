# Manual measurement results

Recorded 27 September 2026. Sources: single-image.har and 1.har–5.har exported manually from Firefox Network. The server was configured with a five-second artificial worker delay. All durations include that delay where applicable.

| Run | Ack ms | Queue ms | Processing ms | Job ms | GETs | Detection ms |
|---|---:|---:|---:|---:|---:|---:|
| Single | 1,064.00 | 994.95 | 6,201.43 | 7,196.38 | 5 | 280.50 |
| Burst 1 | 1,820.00 | 428.66 | 7,309.67 | 7,738.34 | 5 | 59.24 |
| Burst 2 | 73.00 | 5,087.93 | 5,098.28 | 10,186.21 | 5 | 494.01 |
| Burst 3 | 923.00 | 6,478.98 | 9,343.67 | 15,822.65 | 11 | 2,427.61 |
| Burst 4 | 739.00 | 12,869.90 | 6,144.36 | 19,014.26 | 12 | 6.02 |
| Burst 5 | 36.00 | 16,800.34 | 5,249.93 | 22,050.27 | 15 | 536.14 |

Acknowledgement uses the POST's HAR duration through response completion. Queue wait = started_at − queued_at. Processing = completed_at − started_at. Job duration = completed_at − queued_at. GET count includes only status requests for that job. Detection is approximated by the first completed GET's start time plus HAR duration, minus completed_at; it does not measure UI painting. Browser and server ran on the same host.

The five submissions span 13.083 seconds. This is a manually staggered five-tab burst, not simultaneous submissions. All five are distinct completed jobs. Their processing intervals do not overlap, and later jobs wait progressively longer. No tighter burst is claimed.

The original HAR files remain in Downloads. A compact extract without upload bodies or unrelated headers is stored in manual-evidence/measurements.json.

## Reproduced extraction method — 4 October 2026

The original extraction tool was not documented. An AI-assisted Python script, `scripts/measure-har.py`, was saved and run against the six original HAR files to establish a reproducible method. This reanalysed the existing recordings; it did not perform new browser tests. Every regenerated record exactly matched `manual-evidence/measurements.json`, including all six metrics, job IDs, timestamps and request lists. The new output is `manual-evidence/measurements-reproduced.json`.

Run from the project root:

```bash
python3 scripts/measure-har.py /home/daryn-forman/Downloads --output docs/manual-evidence/measurements-reproduced.json --compare docs/manual-evidence/measurements.json
```

The script uses Python's standard library. It reads `log.entries` in each HAR, finds the accepted upload POST to `/v1/images`, and reads `job_id` from its JSON response. It selects status GETs whose URL matches that job, excluding image downloads. It reads the server timestamps from a successful completed-status response.

- Acknowledgement: upload entry's `time` in milliseconds.
- Queue wait: `started_at - queued_at`.
- Processing: `completed_at - started_at`.
- Job duration: `completed_at - queued_at`.
- GET count: number of recorded status GET entries for the job.
- Detection delay: earliest completed-status response end (`startedDateTime + time`) minus server `completed_at`.

Timestamp differences are converted to milliseconds and rounded to two decimal places. The script writes JSON, prints the results, and compares the entire extract with the saved evidence. A mismatch causes an error exit.

Oral explanation: "I manually performed the uploads and exported Firefox's network recordings as HAR files. With AI assistance, I used a Python script to analyse those recordings. It matched requests by job ID, read the upload duration, counted status checks, and subtracted the server timestamps to calculate job timings. It also calculated the gap between server completion and receipt of the first completed-status response. The script reproduced all six report rows exactly."

Manual testing describes how the network activity was generated. Scripted analysis describes how the measurements were extracted; the script does not operate the browser or submit uploads. The five-second worker delay, staggered submissions, and response-end approximation still apply.
