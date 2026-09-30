package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"image"
	"image/color"
	"image/png"
	"imagelab/internal/data"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Check landscape, portrait, and small images, plus the thumbnail center crop.
func TestVariantDimensions(t *testing.T) {
	for _, tc := range []struct{ w, h, pw, ph, dw, dh int }{{1800, 1200, 800, 533, 1200, 800}, {1200, 1800, 400, 600, 600, 900}, {80, 40, 80, 40, 80, 40}} {
		src := image.NewRGBA(image.Rect(0, 0, tc.w, tc.h))
		for _, v := range []struct {
			w, h         int
			crop         bool
			wantW, wantH int
		}{{150, 150, true, 150, 150}, {800, 600, false, tc.pw, tc.ph}, {1200, 900, false, tc.dw, tc.dh}} {
			out := resizeVariant(src, v.w, v.h, v.crop)
			if out.Bounds().Dx() != v.wantW || out.Bounds().Dy() != v.wantH {
				t.Fatalf("source %dx%d: got %v", tc.w, tc.h, out.Bounds())
			}
		}
	}
	src := image.NewRGBA(image.Rect(0, 0, 300, 150))
	for y := 0; y < 150; y++ {
		for x := 75; x < 225; x++ {
			src.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	out := resizeVariant(src, 150, 150, true)
	if out.RGBAAt(0, 0).R != 255 || out.RGBAAt(149, 149).R != 255 {
		t.Fatal("thumbnail must center crop")
	}
}

// Uses a disposable schema, so it never modifies existing application tables.
// Exercise the upload, database, worker, and output endpoints together.
// A missing test original later checks that processing errors produce failed status.
func TestWeek2Integration(t *testing.T) {
	dsn := os.Getenv("IMAGELAB_TEST_DSN")
	if dsn == "" {
		t.Skip("set IMAGELAB_TEST_DSN to run PostgreSQL integration checks")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	schema := fmt.Sprintf("imagelab_test_%d", time.Now().UnixNano())
	if _, err = db.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DROP SCHEMA " + schema + " CASCADE")
	if _, err = db.Exec("SET search_path TO " + schema); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"000001_create_images", "000002_create_jobs", "000003_create_variants"} {
		b, e := os.ReadFile(filepath.Join("../../migrations", name+".up.sql"))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(string(b)); e != nil {
			t.Fatal(e)
		}
	}
	app := &application{models: data.NewModels(db), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	app.config.storageDir = t.TempDir()
	app.config.workerDelay = 100 * time.Millisecond
	if err = os.MkdirAll(filepath.Join(app.config.storageDir, "originals"), 0750); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1800, 1200))); err != nil {
		t.Fatal(err)
	}
	submit := func(payload []byte) *httptest.ResponseRecorder {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		part, e := mw.CreateFormFile("image", "example.png")
		if e != nil {
			t.Fatal(e)
		}
		part.Write(payload)
		mw.Close()
		r := httptest.NewRequest("POST", "/v1/images", &b)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		app.routes().ServeHTTP(w, r)
		return w
	}
	// Invalid uploads must leave neither database records nor original files.
	for _, tc := range []struct {
		name    string
		payload []byte
		missing bool
	}{
		{name: "empty"},
		{name: "unsupported", payload: []byte("not an image")},
		{name: "corrupt_png", payload: encoded.Bytes()[:40]},
		{name: "oversized", payload: make([]byte, maxImageBytes+1)},
		{name: "missing", missing: true},
	} {
		if !t.Run(tc.name, func(t *testing.T) {
			var rejected *httptest.ResponseRecorder
			if tc.missing {
				rejected = httptest.NewRecorder()
				app.routes().ServeHTTP(rejected, httptest.NewRequest("POST", "/v1/images", nil))
			} else {
				rejected = submit(tc.payload)
			}
			if rejected.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rejected.Code, rejected.Body.String())
			}
			var images, jobs int
			if err := db.QueryRow("SELECT (SELECT count(*) FROM images),(SELECT count(*) FROM jobs)").Scan(&images, &jobs); err != nil {
				t.Fatal(err)
			}
			if images != 0 || jobs != 0 {
				t.Fatalf("rejection created records: images=%d jobs=%d", images, jobs)
			}
			files, err := os.ReadDir(filepath.Join(app.config.storageDir, "originals"))
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 0 {
				t.Fatal("rejection left stored originals")
			}
		}) {
			t.FailNow()
		}
	}
	w := submit(encoded.Bytes())
	if w.Code != 202 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var accepted struct {
		ImageID   uuid.UUID `json:"image_id"`
		JobID     uuid.UUID `json:"job_id"`
		StatusURL string    `json:"status_url"`
		Status    string    `json:"status"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Status != "queued" || w.Header().Get("Location") != accepted.StatusURL {
		t.Fatal("invalid acceptance contract")
	}
	ctx := context.Background()
	j, err := app.models.Jobs.Get(ctx, accepted.JobID)
	if err != nil || j.Status != "queued" {
		t.Fatalf("durable queued job: %v %v", j, err)
	}
	if j.ID.Version() != 7 || j.ImageID.Version() != 7 {
		t.Fatal("image and job IDs must be UUID v7")
	}
	if accepted.JobID.Version() != 4 || accepted.ImageID.Version() != 4 || accepted.JobID != j.PublicID || accepted.ImageID != j.ImagePublicID {
		t.Fatal("API must return public UUID v4 IDs")
	}
	if accepted.StatusURL != "/v1/jobs/"+j.PublicID.String() {
		t.Fatal("status URL must use public ID")
	}
	// Internal IDs must not resolve through public API endpoints.
	for _, path := range []string{"/v1/jobs/" + j.ID.String(), "/v1/images/" + j.ImageID.String() + "/variants/thumbnail"} {
		response := httptest.NewRecorder()
		app.routes().ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("internal ID resolved: %s", path)
		}
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); app.runWorker(workerCtx) }()
	defer func() { cancel(); <-done }()
	await := func(id uuid.UUID, want string) *data.Job {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			j, e := app.models.Jobs.Get(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			if j.Status == want {
				return j
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("job %s did not reach %s", id, want)
		return nil
	}
	await(j.PublicID, "processing")
	j = await(j.PublicID, "completed")
	if len(j.Variants) != 3 || j.StartedAt == nil || j.CompletedAt == nil || j.CompletedAt.Before(*j.StartedAt) {
		t.Fatalf("invalid completed job: %+v", j)
	}
	var variantID uuid.UUID
	if err = db.QueryRow("SELECT id FROM variants LIMIT 1").Scan(&variantID); err != nil || variantID.Version() != 7 {
		t.Fatalf("expected UUID v7 variant ID: %s, %v", variantID, err)
	}
	for _, v := range j.Variants {
		if v.URL != fmt.Sprintf("/v1/images/%s/variants/%s", j.ImagePublicID, v.Name) {
			t.Fatal("variant URL must use public image ID")
		}
		w := httptest.NewRecorder()
		app.routes().ServeHTTP(w, httptest.NewRequest("GET", v.URL, nil))
		if w.Code != http.StatusOK {
			t.Fatal(w.Code)
		}
		cfg, _, e := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		if e != nil || cfg.Width != v.Width || cfg.Height != v.Height {
			t.Fatalf("variant metadata does not match file: %v", e)
		}
	}
	w = httptest.NewRecorder()
	app.routes().ServeHTTP(w, httptest.NewRequest("GET", accepted.StatusURL, nil))
	if w.Code != 200 {
		t.Fatal("job endpoint")
	}
	var statusResponse struct {
		ID      uuid.UUID `json:"id"`
		ImageID uuid.UUID `json:"image_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &statusResponse); err != nil {
		t.Fatal(err)
	}
	if statusResponse.ID != j.PublicID || statusResponse.ImageID != j.ImagePublicID {
		t.Fatal("status response exposed internal IDs")
	}
	// Stop observation-independent worker, accept another job, and remove only its input.
	cancel()
	<-done
	w = submit(encoded.Bytes())
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &accepted)
	var stored string
	if err = db.QueryRow(`SELECT stored_filename FROM images JOIN jobs ON images.id=jobs.image_id WHERE jobs.public_id=$1`, accepted.JobID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(app.config.storageDir, "originals", stored)); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel = context.WithCancel(ctx)
	defer cancel()
	done = make(chan struct{})
	go func() { defer close(done); app.runWorker(workerCtx) }()
	j = await(accepted.JobID, "failed")
	if j.Error == nil || j.FailedAt == nil || j.CompletedAt != nil || len(j.Variants) != 0 {
		t.Fatalf("invalid failure: %+v", j)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM images").Scan(&count); err != nil || count != 2 {
		t.Fatalf("rejected input created metadata: %d %v", count, err)
	}
}

func TestInvalidUUIDRoutes(t *testing.T) {
	app := &application{}
	for _, id := range []string{"1", "not-a-uuid", uuid.Nil.String()} {
		for _, path := range []string{"/v1/jobs/" + id, "/v1/images/" + id + "/variants/thumbnail"} {
			w := httptest.NewRecorder()
			app.routes().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s: got %d", path, w.Code)
			}
		}
	}
}
