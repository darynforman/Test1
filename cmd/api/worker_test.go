package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	w := submit([]byte("not an image"))
	if w.Code != 400 {
		t.Fatalf("invalid upload: %d", w.Code)
	}
	w = submit(encoded.Bytes())
	if w.Code != 202 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var accepted struct {
		JobID     int64  `json:"job_id"`
		StatusURL string `json:"status_url"`
		Status    string `json:"status"`
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
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); app.runWorker(workerCtx) }()
	defer func() { cancel(); <-done }()
	await := func(id int64, want string) *data.Job {
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
		t.Fatalf("job %d did not reach %s", id, want)
		return nil
	}
	await(j.ID, "processing")
	j = await(j.ID, "completed")
	if len(j.Variants) != 3 || j.StartedAt == nil || j.CompletedAt == nil || j.CompletedAt.Before(*j.StartedAt) {
		t.Fatalf("invalid completed job: %+v", j)
	}
	for _, v := range j.Variants {
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
	// Stop observation-independent worker, accept another job, and remove only its input.
	cancel()
	<-done
	w = submit(encoded.Bytes())
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &accepted)
	var stored string
	if err = db.QueryRow(`SELECT stored_filename FROM images JOIN jobs ON images.id=jobs.image_id WHERE jobs.id=$1`, accepted.JobID).Scan(&stored); err != nil {
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
