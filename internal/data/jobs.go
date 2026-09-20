package data

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"time"
)

// Job holds progress information read from PostgreSQL.
// Pointer fields can be nil when an event, such as completion, has not happened yet.
type Job struct {
	ID          uuid.UUID  `json:"id"`
	ImageID     uuid.UUID  `json:"image_id"`
	Status      string     `json:"status"`
	Error       *string    `json:"error,omitempty"`
	QueuedAt    time.Time  `json:"queued_at"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	FailedAt    *time.Time `json:"failed_at"`
	Variants    []Variant  `json:"variants,omitempty"`
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
type JobModel struct{ DB *sql.DB }

// Accept commits both records together; a failed job insert cannot leave an accepted image.
func (m JobModel) Accept(ctx context.Context, img *Image) (*Job, error) {
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Undo unfinished changes if we return early. After Commit, this does nothing.
	defer tx.Rollback()
	// Save the image first so the job can refer to its database-generated ID.
	err = tx.QueryRowContext(ctx, `INSERT INTO images (original_filename,stored_filename,media_type,size_bytes) VALUES ($1,$2,$3,$4) RETURNING id,created_at`, img.OriginalFilename, img.StoredFilename, img.MediaType, img.SizeBytes).Scan(&img.ID, &img.CreatedAt)
	if err != nil {
		return nil, err
	}
	j := &Job{ImageID: img.ID, Status: "queued"}
	err = tx.QueryRowContext(ctx, `INSERT INTO jobs (image_id) VALUES ($1) RETURNING id,queued_at`, img.ID).Scan(&j.ID, &j.QueuedAt)
	if err != nil {
		return nil, err
	}
	// Commit saves both records together; until then, the worker cannot see this job.
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return j, nil
}

// Read the current state immediately; do not wait here for processing to finish.
func (m JobModel) Get(ctx context.Context, id uuid.UUID) (*Job, error) {
	j := new(Job)
	err := m.DB.QueryRowContext(ctx, `SELECT id,image_id,status,error_message,queued_at,started_at,completed_at,failed_at FROM jobs WHERE id=$1`, id).Scan(&j.ID, &j.ImageID, &j.Status, &j.Error, &j.QueuedAt, &j.StartedAt, &j.CompletedAt, &j.FailedAt)
	if err != nil {
		return nil, err
	}
	// Only include result details after the whole job has completed.
	if j.Status == "completed" {
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
			v.URL = fmt.Sprintf("/v1/images/%s/variants/%s", j.ImageID, v.Name)
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
	err := m.DB.QueryRowContext(ctx, `WITH next AS (SELECT id FROM jobs WHERE status='queued' ORDER BY queued_at,id FOR UPDATE SKIP LOCKED LIMIT 1), claimed AS (UPDATE jobs SET status='processing',started_at=clock_timestamp() FROM next WHERE jobs.id=next.id RETURNING jobs.id,jobs.image_id) SELECT claimed.id,claimed.image_id,images.stored_filename FROM claimed JOIN images ON images.id=claimed.image_id`).Scan(&j.ID, &j.ImageID, &stored)
	return j, stored, err
}

// The worker calls this after writing all three files.
// Save their metadata and completed status together so partial results are not published.
func (m JobModel) Complete(ctx context.Context, j *Job, variants []Variant) error {
	if len(variants) != 3 {
		return fmt.Errorf("expected three variants")
	}
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range variants {
		_, err = tx.ExecContext(ctx, `INSERT INTO variants (image_id,name,stored_filename,width,height,size_bytes) VALUES ($1,$2,$3,$4,$5,$6)`, j.ImageID, v.Name, v.StoredFilename, v.Width, v.Height, v.SizeBytes)
		if err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET status='completed',completed_at=clock_timestamp() WHERE id=$1 AND status='processing'`, j.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("job is no longer processing")
	}
	return tx.Commit()
}

// Save a simple error for the user; detailed technical errors stay in the server log.
func (m JobModel) Fail(ctx context.Context, id uuid.UUID) error {
	_, err := m.DB.ExecContext(ctx, `UPDATE jobs SET status='failed',failed_at=clock_timestamp(),error_message='Image processing could not finish. Please submit the image again.' WHERE id=$1 AND status='processing'`, id)
	return err
}

// Find only a known output belonging to a completed job.
func (m JobModel) Variant(ctx context.Context, imageID uuid.UUID, name string) (string, error) {
	var stored string
	err := m.DB.QueryRowContext(ctx, `SELECT v.stored_filename FROM variants v WHERE v.image_id=$1 AND v.name=$2 AND EXISTS (SELECT 1 FROM jobs j WHERE j.image_id=v.image_id AND j.status='completed')`, imageID, name).Scan(&stored)
	return stored, err
}
