package data

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"log/slog"
	"time"
)

// Job holds progress information read from PostgreSQL.
// Pointer fields can be nil when an event, such as completion, has not happened yet.
type Job struct {
	Version           int64      `json:"version"`
	Stage             string     `json:"stage"`
	VariantsCompleted int        `json:"variants_completed"`
	SimulationStep    int        `json:"simulation_step"`
	ID                uuid.UUID  `json:"-"`
	PublicID          uuid.UUID  `json:"id"`
	ImageID           uuid.UUID  `json:"-"`
	ImagePublicID     uuid.UUID  `json:"image_id"`
	Status            string     `json:"status"`
	Error             *string    `json:"error,omitempty"`
	QueuedAt          time.Time  `json:"queued_at"`
	StartedAt         *time.Time `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	FailedAt          *time.Time `json:"failed_at"`
	Variants          []Variant  `json:"variants,omitempty"`
}

// Variant describes one generated image. json:"-" keeps internal fields out of JSON.
type Variant struct {
	ID             uuid.UUID `json:"-"`
	ImageID        uuid.UUID `json:"-"`
	Name           string    `json:"name"`
	StoredFilename string    `json:"-"`
	Width          int       `json:"width"`
	Height         int       `json:"height"`
	SizeBytes      int64     `json:"size_bytes"`
	URL            string    `json:"url"`
}
type JobModel struct {
	DB      *sql.DB
	Changes *JobChanges
}

// Accept commits both records together; a failed job insert cannot leave an accepted image.
func (m JobModel) Accept(ctx context.Context, img *Image) (*Job, error) {
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Undo unfinished changes if we return early. After Commit, this does nothing.
	defer tx.Rollback()
	// Save the image first so the job can refer to its database-generated ID.
	err = tx.QueryRowContext(ctx, `INSERT INTO images (original_filename,stored_filename,media_type,size_bytes) VALUES ($1,$2,$3,$4) RETURNING id,public_id,created_at`, img.OriginalFilename, img.StoredFilename, img.MediaType, img.SizeBytes).Scan(&img.ID, &img.PublicID, &img.CreatedAt)
	if err != nil {
		return nil, err
	}
	j := &Job{ImageID: img.ID, ImagePublicID: img.PublicID, Status: "queued", Version: 1, Stage: "starting"}
	err = tx.QueryRowContext(ctx, `INSERT INTO jobs (image_id) VALUES ($1) RETURNING id,public_id,queued_at`, img.ID).Scan(&j.ID, &j.PublicID, &j.QueuedAt)
	if err != nil {
		return nil, err
	}
	// Commit saves both records together; until then, the worker cannot see this job.
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	m.committed(j)
	return j, nil
}

// Look up the public v4 ID; internal IDs remain available for database work.
// Read the current state immediately; do not wait here for processing to finish.
func (m JobModel) Get(ctx context.Context, publicID uuid.UUID) (*Job, error) {
	return m.GetObserved(ctx, publicID, func() {})
}

// Count actual SQL reads, including terminal variant metadata retrieval.
func (m JobModel) GetObserved(ctx context.Context, publicID uuid.UUID, read func()) (*Job, error) {
	read()
	j := new(Job)
	err := m.DB.QueryRowContext(ctx, `SELECT j.id,j.public_id,j.image_id,i.public_id,j.status,j.error_message,j.queued_at,j.started_at,j.completed_at,j.failed_at,j.version,j.stage,j.variants_completed,j.simulation_step FROM jobs j JOIN images i ON i.id=j.image_id WHERE j.public_id=$1`, publicID).Scan(&j.ID, &j.PublicID, &j.ImageID, &j.ImagePublicID, &j.Status, &j.Error, &j.QueuedAt, &j.StartedAt, &j.CompletedAt, &j.FailedAt, &j.Version, &j.Stage, &j.VariantsCompleted, &j.SimulationStep)
	if err != nil {
		return nil, err
	}
	// Only include result details after the whole job has completed.
	if j.Status == "completed" {
		read()
		rows, err := m.DB.QueryContext(ctx, `SELECT name,width,height,size_bytes FROM variants WHERE image_id=$1 ORDER BY CASE name WHEN 'thumbnail' THEN 1 WHEN 'preview' THEN 2 ELSE 3 END`, j.ImageID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var v Variant
			if err = rows.Scan(&v.Name, &v.Width, &v.Height, &v.SizeBytes); err != nil {
				return nil, err
			}
			v.URL = fmt.Sprintf("/v1/images/%s/variants/%s", j.ImagePublicID, v.Name)
			j.Variants = append(j.Variants, v)
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	return j, nil
}

// Claim changes the oldest queued job atomically before any filesystem work starts.
func (m JobModel) Claim(ctx context.Context) (*Job, string, error) {
	j := new(Job)
	var stored string
	// Pick the oldest queued job and mark it processing in the same SQL statement.
	// Locking the selected row prevents another claimant from taking the same job.
	err := m.DB.QueryRowContext(ctx, `WITH next AS (SELECT id FROM jobs WHERE status='queued' ORDER BY queued_at,id FOR UPDATE SKIP LOCKED LIMIT 1), claimed AS (UPDATE jobs SET status='processing',started_at=clock_timestamp(),version=version+1 FROM next WHERE jobs.id=next.id RETURNING jobs.id,jobs.public_id,jobs.image_id,jobs.version,jobs.stage,jobs.variants_completed,jobs.simulation_step) SELECT claimed.id,claimed.public_id,claimed.image_id,claimed.version,claimed.stage,claimed.variants_completed,claimed.simulation_step,images.stored_filename FROM claimed JOIN images ON images.id=claimed.image_id`).Scan(&j.ID, &j.PublicID, &j.ImageID, &j.Version, &j.Stage, &j.VariantsCompleted, &j.SimulationStep, &stored)
	if err == nil {
		j.Status = "processing"
		m.committed(j)
	}
	return j, stored, err
}

// Publish only committed changes. The timestamp is captured before logging or notification.
func (m JobModel) committed(j *Job) {
	now := time.Now()
	slog.Info("job_commit", "job_id", j.PublicID, "version", j.Version, "status", j.Status, "stage", j.Stage, "variants_completed", j.VariantsCompleted, "simulation_step", j.SimulationStep, "commit_ms", now.UnixMilli())
	if m.Changes != nil {
		m.Changes.Notify(j.PublicID)
	}
}

// A milestone stores actual file metadata and truthful progress in one transaction.
func (m JobModel) Milestone(ctx context.Context, j *Job, v Variant) error {
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO variants (image_id,name,stored_filename,width,height,size_bytes) VALUES ($1,$2,$3,$4,$5,$6)`, j.ImageID, v.Name, v.StoredFilename, v.Width, v.Height, v.SizeBytes)
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `UPDATE jobs SET variants_completed=variants_completed+1,stage=$2,version=version+1 WHERE id=$1 AND status='processing' AND variants_completed<3 RETURNING version,variants_completed`, j.ID, v.Name+"_ready").Scan(&j.Version, &j.VariantsCompleted)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	j.Stage = v.Name + "_ready"
	j.Status = "processing"
	m.committed(j)
	return nil
}

func (m JobModel) Complete(ctx context.Context, j *Job, variants []Variant) error {
	if len(variants) != 3 {
		return fmt.Errorf("expected three variants")
	}
	err := m.DB.QueryRowContext(ctx, `UPDATE jobs SET status='completed',completed_at=clock_timestamp(),version=version+1 WHERE id=$1 AND status='processing' AND variants_completed=3 AND (SELECT count(*) FROM variants WHERE image_id=$2)=3 RETURNING version`, j.ID, j.ImageID).Scan(&j.Version)
	if err != nil {
		return err
	}
	j.Status = "completed"
	m.committed(j)
	return nil
}

func (m JobModel) Simulate(ctx context.Context, j *Job, step int) error {
	err := m.DB.QueryRowContext(ctx, `UPDATE jobs SET simulation_step=$2,version=version+1 WHERE id=$1 AND status='processing' AND simulation_step<>$2 RETURNING version`, j.ID, step).Scan(&j.Version)
	if err != nil {
		return err
	}
	j.SimulationStep = step
	m.committed(j)
	return nil
}

func (m JobModel) Fail(ctx context.Context, id uuid.UUID) error {
	j := &Job{ID: id, Status: "failed"}
	err := m.DB.QueryRowContext(ctx, `UPDATE jobs SET status='failed',failed_at=clock_timestamp(),version=version+1,error_message='Image processing could not finish. Please submit the image again.' WHERE id=$1 AND status='processing' RETURNING public_id,version,stage,variants_completed,simulation_step`, id).Scan(&j.PublicID, &j.Version, &j.Stage, &j.VariantsCompleted, &j.SimulationStep)
	if err != nil {
		return err
	}
	m.committed(j)
	return nil
}

// Find only a known output belonging to a completed job.
func (m JobModel) Variant(ctx context.Context, imagePublicID uuid.UUID, name string) (string, error) {
	var stored string
	err := m.DB.QueryRowContext(ctx, `SELECT v.stored_filename FROM variants v JOIN images i ON i.id=v.image_id WHERE i.public_id=$1 AND v.name=$2 AND EXISTS (SELECT 1 FROM jobs j WHERE j.image_id=v.image_id AND j.status='completed')`, imagePublicID, name).Scan(&stored)
	return stored, err
}
