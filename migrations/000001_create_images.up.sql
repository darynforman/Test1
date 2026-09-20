CREATE TABLE images (
 id UUID PRIMARY KEY DEFAULT uuidv7(),
 original_filename TEXT NOT NULL,
 stored_filename TEXT NOT NULL UNIQUE,
 media_type TEXT NOT NULL CHECK (media_type IN ('image/jpeg','image/png')),
 size_bytes BIGINT NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
