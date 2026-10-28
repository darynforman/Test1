package main

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"imagelab/internal/data"
	"net/http"
	"path/filepath"
	"strconv"
	"time"
)

// Return the job status and timestamps so the client can check its progress.
func (app *application) showJobHandler(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("job_id"))
	if err != nil || id == uuid.Nil {
		app.notFoundResponse(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	// No "after" means a prompt snapshot. With "after", use the long-polling path below.
	after, wait, long, err := observationQuery(r)
	if err != nil {
		app.errorResponse(w, r, http.StatusBadRequest, err.Error())
		return
	}
	reads := 0
	defer func() { app.logger.Info("observation", "job_id", id, "db_reads", reads) }()
	get := func() (*data.Job, error) { return app.models.Jobs.GetObserved(r.Context(), id, func() { reads++ }) }
	if long {
		// LONG POLLING: subscribe BEFORE reading PostgreSQL so we cannot miss a change
		// between checking the job and starting to wait. Each observer gets a buffered signal.
		changed, release := app.models.Jobs.Changes.Subscribe(id)
		// Remove this observer when the request ends; do not leave a subscription behind.
		defer release()
		// This is the holding deadline, not a timer for finishing image processing.
		returnAfter := time.NewTimer(wait)
		defer returnAfter.Stop()
		for {
			// PostgreSQL is the source of truth. A notification only tells us to read again.
			job, err := get()
			if !app.jobReadOK(w, r, err) {
				return
			}
			if after > job.Version {
				// The browser cannot have seen a future version, so reject it instead of waiting.
				app.errorResponse(w, r, http.StatusBadRequest, "after exceeds current version")
				return
			}
			// Reply now if the job is newer. Completed and failed jobs also reply immediately,
			// even if the browser already has that version, so it can stop observing.
			if job.Version > after || job.Status == "completed" || job.Status == "failed" {
				app.writeJob(w, r, job)
				return
			}
			// Unchanged and still active: wait for a signal, cancellation, or the deadline.
			// There is no timer repeatedly querying the database while this select waits.
			select {
			case <-changed:
				// Go back to the top of the loop and read the actual saved state.
			case <-r.Context().Done():
				// The browser stopped observing. End this request, not the worker's job.
				return
			case <-returnAfter.C:
				// Check once more at the deadline: a change may have just committed.
				latest, err := get()
				if !app.jobReadOK(w, r, err) {
					return
				}
				if latest.Version > after || latest.Status == "completed" || latest.Status == "failed" {
					app.writeJob(w, r, latest)
				} else {
					// Still unchanged: 204 means "no new snapshot", with no JSON body.
					// The browser should keep its version and immediately send another long poll.
					w.WriteHeader(http.StatusNoContent)
				}
				return
			}
		}
	}
	// SHORT POLLING (also the initial snapshot in long mode): read once and reply now.
	// The browser controls the one-second delay before its next short-poll request.
	job, err := get()
	if app.jobReadOK(w, r, err) {
		app.writeJob(w, r, job)
	}
}

// Parse explicit integer seconds; absence of query parameters is always prompt.
func observationQuery(r *http.Request) (int64, time.Duration, bool, error) {
	q := r.URL.Query()
	if !q.Has("after") {
		if q.Has("wait") {
			return 0, 0, false, fmt.Errorf("wait requires after")
		}
		return 0, 0, false, nil
	}
	parse := func(key string) (int64, error) {
		values := q[key]
		if len(values) != 1 || values[0] == "" {
			return 0, fmt.Errorf("invalid %s", key)
		}
		for _, c := range values[0] {
			if c < '0' || c > '9' {
				return 0, fmt.Errorf("invalid %s", key)
			}
		}
		return strconv.ParseInt(values[0], 10, 64)
	}
	after, err := parse("after")
	if err != nil {
		return 0, 0, false, fmt.Errorf("after must be a nonnegative integer")
	}
	seconds := int64(20)
	if q.Has("wait") {
		seconds, err = parse("wait")
	}
	if err != nil || seconds < 1 || seconds > 20 {
		return 0, 0, false, fmt.Errorf("wait must be an integer from 1 to 20")
	}
	return after, time.Duration(seconds) * time.Second, true, nil
}
func (app *application) jobReadOK(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, sql.ErrNoRows) {
		app.notFoundResponse(w, r)
		return false
	}
	if err != nil {
		if r.Context().Err() == nil {
			app.serverErrorResponse(w, r, err)
		}
		return false
	}
	return true
}
func (app *application) writeJob(w http.ResponseWriter, r *http.Request, job *data.Job) {
	if err := app.writeJSON(w, http.StatusOK, job, nil); err != nil {
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
