ALTER TABLE jobs
 ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
 ADD COLUMN stage TEXT NOT NULL DEFAULT 'starting',
 ADD COLUMN variants_completed INTEGER NOT NULL DEFAULT 0 CHECK (variants_completed BETWEEN 0 AND 3),
 ADD COLUMN simulation_step INTEGER NOT NULL DEFAULT 0 CHECK (simulation_step >= 0);
UPDATE jobs SET stage='display_ready', variants_completed=3 WHERE status='completed';
