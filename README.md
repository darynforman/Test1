# ImageLab - Assessment 1, Week 2

Go, PostgreSQL, HTML, CSS, and vanilla JavaScript. Extends the Week 1 project with durable asynchronous image processing.

## Implemented

- Local JPEG/PNG preview, server validation up to 10 MB, and overlapping-submit protection.
- Server-controlled original filenames; one transaction creates the image and queued job.
- `POST /v1/images` returns `202 Accepted`, `Location`, `image_id`, `job_id`, `status`, and `status_url` after that transaction commits.
- `GET /v1/jobs/{job_id}` returns current PostgreSQL state and timestamps immediately.
- Exactly one in-process worker claims queued jobs in order, records `started_at`, and generates three JPEG outputs.
- Thumbnail: center-cropped 150 × 150. Preview: fits 800 × 600. Display: fits 1200 × 900. Fit profiles preserve proportions without enlarging small inputs; transparent inputs use a white background.
- Variant metadata and completed state commit together after all files exist. Processing failures record a safe error and `failed_at`.
- `GET /v1/images/{image_id}/variants/{name}` serves known variants only after completion.

The browser displays the accepted job and status URL. Automatic polling, lifecycle rendering, and observation recovery are Week 3 work.

## Setup

Prerequisites: Go 1.22 or newer and a running PostgreSQL server. Run commands from the repository root.

```bash
createdb imagelab
export IMAGELAB_DB_DSN='postgres://USER:PASSWORD@localhost/imagelab?sslmode=disable'
# Fresh database only:
for migration in migrations/*.up.sql; do
  psql "$IMAGELAB_DB_DSN" -v ON_ERROR_STOP=1 -f "$migration" || break
done
go run ./cmd/api -db-dsn="$IMAGELAB_DB_DSN"
```

Open <http://localhost:4000>. Files are stored under `storage/originals` and `storage/variants`. Start only one application instance to preserve the assessment's one-worker model.

For visibly slower processing during a check-in:

```bash
go run ./cmd/api -db-dsn="$IMAGELAB_DB_DSN" -worker-delay=5s
```

## Verify

```bash
GOCACHE=/tmp/imagelab-go-cache go test ./...
GOCACHE=/tmp/imagelab-go-cache go vet ./...
# Optional database test: uses and removes a unique schema, requiring CREATE permission.
IMAGELAB_TEST_DSN="$IMAGELAB_DB_DSN" GOCACHE=/tmp/imagelab-go-cache go test ./cmd/api -run TestWeek2Integration -v
```

The integration test verifies upload rejection, durable acceptance, queued → processing → completed, downloadable output dimensions, and a deliberately missing original producing failed. Without a test DSN this test is explicitly skipped.

## Week 2 demonstration

```bash
curl -i -F 'image=@/path/to/photo.png' http://localhost:4000/v1/images
# Use the status_url returned by the POST:
curl http://localhost:4000/v1/jobs/1
psql "$IMAGELAB_DB_DSN" -c 'SELECT id,image_id,status,queued_at,started_at,completed_at,failed_at,error_message FROM jobs ORDER BY id;'
```

With the five-second delay, the response arrives before transformation completes. Repeat the status request to demonstrate state changes; these commands are check-in diagnostics. Browser observation will be automatic in Week 3. The completed response includes all three variant URLs and actual dimensions.

For the induced failure, run the integration test: it removes only a newly created test original before the worker reads it, then checks `failed`, a safe error, and no completed timestamp. No real uploaded files are touched.

## Responsibility boundaries and limitations

The browser owns the local selection. The handler validates and durably accepts work. PostgreSQL owns job state. One worker performs transformations independently of the request. The filesystem stores the bytes.

202 guarantees acceptance, not successful processing. Queued jobs survive application restarts. A hard process crash can leave a job in processing; automatic retries and crash recovery are not implemented in this assessment version. A database outage can also prevent recording a failure; that error is logged. Resize sampling is deliberately simple and can look less smooth than a dedicated image library.

The earlier measurement lab is used as the Go/PostgreSQL structural base because no separate ImageLab starter was supplied.

## Table migrations

Each table has its own up/down migration: `000001` images, `000002` jobs, and `000003` variants. Apply up migrations in that order; roll back in reverse order because jobs and variants reference images. Down migrations delete the corresponding data.

If your Week 1 database already has all three tables from `000001_create_imagelab.up.sql`, **do not rerun these create-table migrations**. The schema is unchanged, so continue using that database. The setup loop above is for a fresh database. If you use a migration tracking tool, reconcile its recorded version with the three existing tables before running further migrations.
