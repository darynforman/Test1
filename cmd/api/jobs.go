package main

import (
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
)

func (app *application) showJobHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("job_id"), 10, 64)
	if err != nil || id < 1 {
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
	w.Header().Set("Cache-Control", "no-store")
	if err = app.writeJSON(w, http.StatusOK, envelope{"id": job.ID, "image_id": job.ImageID, "status": job.Status, "error": job.Error, "queued_at": job.QueuedAt, "started_at": job.StartedAt, "completed_at": job.CompletedAt, "failed_at": job.FailedAt, "variants": job.Variants}, nil); err != nil {
		app.logger.Error("write job response", "error", err)
	}
}
func (app *application) showVariantHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("image_id"), 10, 64)
	name := r.PathValue("name")
	if err != nil || id < 1 || (name != "thumbnail" && name != "preview" && name != "display") {
		app.notFoundResponse(w, r)
		return
	}
	stored, err := app.models.Jobs.Variant(r.Context(), id, name)
	if errors.Is(err, sql.ErrNoRows) {
		app.notFoundResponse(w, r)
		return
	}
	if err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}
	if filepath.Base(stored) != stored {
		app.notFoundResponse(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, filepath.Join(app.config.storageDir, "variants", stored))
}
