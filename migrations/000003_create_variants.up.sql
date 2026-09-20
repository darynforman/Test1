CREATE TABLE variants (
 id UUID PRIMARY KEY DEFAULT uuidv7(),
 image_id UUID NOT NULL REFERENCES images(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK (name IN ('thumbnail','preview','display')),
 stored_filename TEXT NOT NULL UNIQUE,
 width INTEGER NOT NULL CHECK (width>0), height INTEGER NOT NULL CHECK (height>0),
 size_bytes BIGINT NOT NULL CHECK (size_bytes>0), created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(image_id,name)
);
