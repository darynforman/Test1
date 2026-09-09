CREATE TABLE images (
 id BIGSERIAL PRIMARY KEY,
 original_filename TEXT NOT NULL,
 stored_filename TEXT NOT NULL UNIQUE,
 media_type TEXT NOT NULL CHECK (media_type IN ('image/jpeg','image/png')),
 size_bytes BIGINT NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE jobs (
 id BIGSERIAL PRIMARY KEY,
 image_id BIGINT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
 status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','processing','completed','failed')),
 error_message TEXT,
 queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 started_at TIMESTAMPTZ, completed_at TIMESTAMPTZ, failed_at TIMESTAMPTZ
);
CREATE INDEX jobs_queued_idx ON jobs (queued_at,id) WHERE status='queued';
CREATE TABLE variants (
 id BIGSERIAL PRIMARY KEY,
 image_id BIGINT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK (name IN ('thumbnail','preview','display')),
 stored_filename TEXT NOT NULL UNIQUE,
 width INTEGER NOT NULL CHECK (width>0), height INTEGER NOT NULL CHECK (height>0),
 size_bytes BIGINT NOT NULL CHECK (size_bytes>0), created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(image_id,name)
);
