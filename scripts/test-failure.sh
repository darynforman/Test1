#!/bin/sh
set -eu

: "${IMAGELAB_DB_DSN:?Set IMAGELAB_DB_DSN or use make test-failure}"

# Insert only demonstration metadata. The worker must discover the missing
# original and record the failure itself; no real uploaded file is removed.
job_id=$(psql "$IMAGELAB_DB_DSN" -X -qAt -v ON_ERROR_STOP=1 <<'SQL'
WITH demo_image AS (
 INSERT INTO images (original_filename, stored_filename, media_type, size_bytes)
 VALUES ('failure-demo.png', 'missing-demo-' || uuidv7()::text || '.png', 'image/png', 1)
 RETURNING id
)
INSERT INTO jobs (image_id) SELECT id FROM demo_image RETURNING id;
SQL
)

printf 'Created demonstration job: %s\nWaiting for the running app to process it...\n' "$job_id"
attempt=0
while [ "$attempt" -lt 30 ]; do
 result=$(psql "$IMAGELAB_DB_DSN" -X -qAt -v ON_ERROR_STOP=1 -v job_id="$job_id" <<'SQL'
SELECT CASE
 WHEN status = 'failed' AND failed_at IS NOT NULL AND error_message IS NOT NULL
  AND started_at IS NOT NULL AND completed_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM variants WHERE image_id = jobs.image_id)
 THEN 'passed'
 ELSE status END
FROM jobs WHERE id = :'job_id'::uuid;
SQL
 )
 if [ "$result" = passed ]; then
  printf 'PASS: the worker recorded a failure, timestamp, and error without completed results.\n'
  psql "$IMAGELAB_DB_DSN" -X -v ON_ERROR_STOP=1 -P pager=off -v job_id="$job_id" <<'SQL'
SELECT id, status, failed_at, error_message FROM jobs WHERE id = :'job_id'::uuid;
SQL
  exit 0
 fi
 if [ "$result" = completed ]; then
  echo 'FAIL: the missing-file job unexpectedly completed.' >&2
  exit 1
 fi
 attempt=$((attempt + 1))
 sleep 1
done

printf 'Timed out; job %s remains in the database (last status: %s).\n' "$job_id" "$result" >&2
echo 'Run make run in another terminal using the same database, then use make db-failures to inspect the result.' >&2
exit 1
