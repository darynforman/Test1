package main

import (
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"path/filepath"
)

// Return the job status and timestamps so the client can check its progress.
func (app *application) showJobHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("job_id"))
	if err != nil || id == uuid.Nil {
		app.notFoundResponse(w, r)
		return
	}
	job, err := app.models.Jobs.Get(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		app.notFoundResponse(w, r)
		return
	}
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}
	// Ask the browser to fetch fresh status instead of reusing an older response.
	w.Header().Set("Cache-Control", "no-store")
	if err = app.writeJSON(w, http.StatusOK, envelope{"id": job.ID, "image_id": job.ImageID, "status": job.Status, "error": job.Error, "queued_at": job.QueuedAt, "started_at": job.StartedAt, "completed_at": job.CompletedAt, "failed_at": job.FailedAt, "variants": job.Variants}, nil); err != nil {
		app.logger.Error("write job response", "error", err)
	}
}

// Serve a generated image using its image ID and one of the three allowed names.
func (app *application) showVariantHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("image_id"))
	name := r.PathValue("name")
	if err != nil || id == uuid.Nil || (name != "thumbnail" && name != "preview" && name != "display") {
		app.notFoundResponse(w, r)
		return
	}
	// Look up the filename in PostgreSQL instead of trusting a user-supplied file path.
	stored, err := app.models.Jobs.Variant(r.Context(), id, name)
	if errors.Is(err, sql.ErrNoRows) {
		app.notFoundResponse(w, r)
		return
	}
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}
	// A stored filename must be just a name, not a path to another folder.
	if filepath.Base(stored) != stored {
		app.notFoundResponse(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, filepath.Join(app.config.storageDir, "variants", stored))
}
