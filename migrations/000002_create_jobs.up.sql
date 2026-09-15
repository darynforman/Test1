CREATE TABLE jobs (
 id BIGSERIAL PRIMARY KEY,
 image_id BIGINT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
 status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','processing','completed','failed')),
 error_message TEXT,
 queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 started_at TIMESTAMPTZ, completed_at TIMESTAMPTZ, failed_at TIMESTAMPTZ
);
CREATE INDEX jobs_queued_idx ON jobs (queued_at,id) WHERE status='queued';
