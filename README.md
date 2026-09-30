# ImageLab — Setup and running instructions

An asynchronous image-processing application built with Go, PostgreSQL, HTML, CSS, and vanilla JavaScript.

## Implemented

- Local JPEG/PNG preview, server validation up to 10 MB, and overlapping-submit protection.
- Server-controlled original filenames; one transaction creates the image and queued job.
- `POST /v1/images` returns `202 Accepted`, `Location`, `image_id`, `job_id`, `status`, and `status_url` after that transaction commits.
- `GET /v1/jobs/{job_id}` returns current PostgreSQL state and timestamps immediately.
- Exactly one in-process worker claims queued jobs in order, records `started_at`, and generates three JPEG outputs.
- Thumbnail: center-cropped 150 × 150. Preview: fits 800 × 600. Display: fits 1200 × 900. Fit profiles preserve proportions without enlarging small inputs; transparent inputs use a white background.
- Variant metadata and completed state commit together after all files exist. Processing failures record a safe error and `failed_at`.
- `GET /v1/images/{image_id}/variants/{name}` serves known variants only after completion.

The browser automatically polls the accepted job every second, renders its lifecycle, and offers Try again when status retrieval fails.

## Setup

These instructions use Ubuntu/Linux, including Ubuntu in a Windows VM or WSL.
Run project commands in the directory containing `go.mod` and `Makefile`.

### 1. Prerequisites

Install these tools before continuing:

- Git and Make.
- Go 1.22 or newer.
- PostgreSQL **18 or newer**, with the server running and `psql`/`createdb` available.
  The migrations use `uuidv7()` and `uuidv4()`; an older PostgreSQL server is not sufficient.
- Node.js 18 or newer for frontend tests. Node is not needed to run the application.

Check the installed tools:

```bash
git --version
make --version
go version
psql --version
node --version
```

`psql --version` checks the client. The database connection check below verifies
the server and the UUID functions used by this project.

### 2. Get the project

If you already have this checkout, use it and skip cloning.

```bash
git clone --branch week4 https://github.com/darynforman/Test1.git
cd Test1
go mod download
```

### 3. Create a local database (first installation only)

The following example uses local PostgreSQL peer authentication: your database
login has the same name as your Linux user. On an Ubuntu installation managed by
systemd, start PostgreSQL if necessary:

```bash
sudo systemctl start postgresql
```

For a new installation, create a login and a database owned by it:

```bash
sudo -u postgres createuser --login "$(id -un)"
sudo -u postgres createdb --owner="$(id -un)" imagelab
```

Skip creating the role or database if it already exists. Do not delete an existing
database to repeat setup. If your existing database belongs to another role, use
that role's connection and apply schema changes as its owner.

Set the connection for this terminal:

```bash
export IMAGELAB_DB_DSN="host=/var/run/postgresql dbname=imagelab user=$(id -un) sslmode=disable"
psql "$IMAGELAB_DB_DSN" -c 'SELECT current_database(), current_user, version();'
psql "$IMAGELAB_DB_DSN" -c 'SELECT uuidv7(), uuidv4();'
```

For a password-authenticated local connection, use your configured host, user and
password instead, for example:

```bash
export IMAGELAB_DB_DSN='postgres://USER:PASSWORD@localhost/imagelab?sslmode=disable'
```

Replace placeholders with your own settings; do not commit credentials. The
`sslmode=disable` examples are for the local demonstration database. Use the TLS
settings required by your provider for a remote database. The application does
not automatically load a `.env` file; set the variable again in new terminals.

### 4. Apply migrations

For a **fresh, empty database**:

```bash
make db-init
make db-tables
```

The three tables should be `images`, `jobs`, and `variants`. `make db-init` applies
the migrations in order in one transaction. It is not an incremental migration
tracker and must not be rerun against existing tables.

For an existing database, skip `make db-init` and follow
[Internal and public IDs](#internal-and-public-ids) only if the public-ID columns
are missing. Existing databases that already have both columns need no update.

### 5. Start and open ImageLab

```bash
make run
```

Leave that terminal running and open <http://localhost:4000> in a browser on the
same machine/VM. Open the app through the Go server, not as a local HTML file.
Select a JPEG or PNG up to 10 MB, then click Process image. The browser should
observe the job and eventually display thumbnail, preview and display variants.

Only run **one application instance per database** to preserve the one-worker
model. Stop it with Ctrl+C. To make processing visible during a demonstration,
stop the existing instance and restart with:

```bash
make run WORKER_DELAY=5s
```

This adds an artificial five-second delay to each job. Disclose it when recording
measurements. For another port, use `make run PORT=4001` and open localhost:4001.

The app creates `storage/originals` and `storage/variants` for image files. Ensure
the project directory is writable. These files are excluded from Git. To choose
another storage location:

```bash
go run ./cmd/api -db-dsn="$IMAGELAB_DB_DSN" -storage-dir=/path/to/writable/storage
```

### 6. Common startup problems

- **Connection refused:** check that PostgreSQL is running and the configured
  host/socket and port are correct.
- **Role/database does not exist:** create the missing role/database or correct
  `IMAGELAB_DB_DSN`.
- **Permission denied applying migrations:** connect as the table/schema owner;
  normal runtime permissions may not allow schema changes.
- **uuidv7/uuidv4 does not exist:** verify that the connected PostgreSQL server is 18+.
- **public_id column missing:** apply the existing-database update below once.
- **Address already in use:** stop the old app before restarting; do not run a
  second worker against the same database.
- **Page will not load after the offline test:** change Firefox Network from
  Offline to No Throttling and reload.
- **node not found during tests:** install Node.js or supply `NODE=/path/to/node`.

## Verify

Go is required for backend tests. Node.js 18 or newer is required for frontend
tests; no npm packages or browser automation framework are needed.

```bash
make test       # Go and frontend tests; no database connection
make test-go    # Go tests only
make test-ui    # Frontend tests only
make vet       # Go static checks

# Use a PostgreSQL role allowed to CREATE schemas in the chosen database:
export IMAGELAB_TEST_DSN="$IMAGELAB_DB_DSN"
make test-integration
make test-all   # Go, frontend and PostgreSQL tests
```

`make test` deliberately disables database integration even when a test DSN is
exported. `make test-integration` fails clearly if IMAGELAB_TEST_DSN is missing
or the connection fails; it does not report a skipped integration test as a pass.
It applies migrations in a disposable schema and uses temporary image storage.
Existing application tables and images are not modified.

Integration checks cover durable acceptance, public IDs, worker completion and
failure, output dimensions, and rejection of missing, empty, unsupported,
corrupt and oversized inputs without creating records or leaving original files.
Frontend tests simulate page events and timers to check polling, cancellation,
retry, no-file submission and double-click protection. Measurements and manual
presentation evidence are separate from these tests.

If Node is not on PATH, pass its executable explicitly:
`make test NODE=/path/to/node`. Direct `go test ./...` retains its existing behavior:
integration runs only when IMAGELAB_TEST_DSN is set.

## API examples

```bash
curl -i -F 'image=@/path/to/photo.png' http://localhost:4000/v1/images
# Use the status_url returned by the POST:
curl http://localhost:4000/v1/jobs/PUBLIC_JOB_UUID
psql "$IMAGELAB_DB_DSN" -c 'SELECT id,image_id,status,queued_at,started_at,completed_at,failed_at,error_message FROM jobs ORDER BY id;'
```

With the five-second delay, the response arrives before transformation completes. Repeat the status request to demonstrate state changes; these commands are optional API diagnostics. The browser observes jobs automatically every second. The completed response includes all three variant URLs and actual dimensions.

For the induced failure, run the integration test: it removes only a newly created test original before the worker reads it, then checks `failed`, a safe error, and no completed timestamp. No real uploaded files are touched.

## Responsibility boundaries and limitations

The browser owns the local selection. The handler validates and durably accepts work. PostgreSQL owns job state. One worker performs transformations independently of the request. The filesystem stores the bytes.

202 guarantees acceptance, not successful processing. Queued jobs survive application restarts. A hard process crash can leave a job in processing; automatic retries and crash recovery are not implemented in this version. A database outage can also prevent recording a failure; that error is logged. Resize sampling is deliberately simple and can look less smooth than a dedicated image library.

## Table migrations

Each table has its own up/down migration: `000001` images, `000002` jobs, and `000003` variants. Apply up migrations in that order; roll back in reverse order because jobs and variants reference images. Down migrations delete the corresponding data.

If your existing database already has all three tables, **do not rerun these create-table migrations**. Keep the existing tables and apply the public-ID update below only if those columns are missing. `make db-init` is for a fresh database. If you use a migration tracking tool, reconcile its recorded version with the three existing tables before running further migrations.

## Internal and public IDs

Images and jobs have an internal UUID v7 primary key (`id`) and a unique public
UUID v4 (`public_id`). Foreign keys and worker updates use internal IDs. API
responses (`id`, `job_id`, and `image_id`) and URLs use public IDs. Variants need
only internal IDs because public requests identify them by image public ID and name.
The original create-table migrations include both columns for fresh databases.
Check the existing columns first using `make db-shell`, then `\d images` and
`\d jobs`. If both tables already have `public_id`, skip the update. If neither
has it, apply the following once as the table owner before running this version
(do not rerun the create-table migrations):

```sql
BEGIN;
ALTER TABLE images ADD COLUMN public_id UUID NOT NULL UNIQUE DEFAULT uuidv4();
ALTER TABLE jobs ADD COLUMN public_id UUID NOT NULL UNIQUE DEFAULT uuidv4();
COMMIT;
```

Existing records receive public IDs automatically; previously issued internal-ID
URLs must be replaced with the new public URLs.
