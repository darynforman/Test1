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
