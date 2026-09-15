package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"imagelab/internal/data"
	"os"
	"path/filepath"
	"time"
)

// One loop performs jobs serially. PostgreSQL is the queue, not a goroutine per upload.
func (app *application) runWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for ctx.Err() == nil {
			job, stored, err := app.models.Jobs.Claim(ctx)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				app.logger.Error("claim job", "error", err)
				break
			}
			app.logger.Info("job processing", "job_id", job.ID)
			err = app.processJob(ctx, job, stored)
			if err != nil {
				app.logger.Error("process job", "job_id", job.ID, "error", err)
				failCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if e := app.models.Jobs.Fail(failCtx, job.ID); e != nil {
					app.logger.Error("record job failure", "job_id", job.ID, "error", e)
				}
				cancel()
			} else {
				app.logger.Info("job completed", "job_id", job.ID)
			}
		}
	}
}
func (app *application) processJob(ctx context.Context, job *data.Job, stored string) error {
	if app.config.workerDelay > 0 {
		timer := time.NewTimer(app.config.workerDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	f, err := os.Open(filepath.Join(app.config.storageDir, "originals", stored))
	if err != nil {
		return err
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return err
	}
	dir := filepath.Join(app.config.storageDir, "variants")
	if err = os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	var variants []data.Variant
	var paths []string
	complete := false
	defer func() {
		if !complete {
			for _, p := range paths {
				_ = os.Remove(p)
			}
		}
	}()
	for _, profile := range []struct {
		name string
		w, h int
		crop bool
	}{{"thumbnail", 150, 150, true}, {"preview", 800, 600, false}, {"display", 1200, 900, false}} {
		if err = ctx.Err(); err != nil {
			return err
		}
		out := resizeVariant(src, profile.w, profile.h, profile.crop)
		name := fmt.Sprintf("%d_%s.jpg", job.ID, profile.name)
		path := filepath.Join(dir, name)
		paths = append(paths, path)
		dst, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
		if e != nil {
			return e
		}
		e = jpeg.Encode(dst, out, &jpeg.Options{Quality: 90})
		closeErr := dst.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		info, e := os.Stat(path)
		if e != nil {
			return e
		}
		variants = append(variants, data.Variant{Name: profile.name, StoredFilename: name, Width: out.Bounds().Dx(), Height: out.Bounds().Dy(), SizeBytes: info.Size()})
	}
	if err = app.models.Jobs.Complete(ctx, job, variants); err != nil {
		return err
	}
	complete = true
	return nil
}

// Center-crop thumbnails; fit the other profiles without enlarging small originals.
// Sampling maps both axes through the same source rectangle, preserving proportions.
func resizeVariant(src image.Image, maxW, maxH int, crop bool) *image.RGBA {
	bounds := src.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	w, h := maxW, maxH
	if crop {
		side := min(sw, sh)
		bounds.Min.X += (sw - side) / 2
		bounds.Min.Y += (sh - side) / 2
		sw, sh = side, side
	} else {
		w, h = sw, sh
		if w > maxW {
			h = max(1, h*maxW/w)
			w = maxW
		}
		if h > maxH {
			w = max(1, w*maxH/h)
			h = maxH
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	// Composite transparent PNG pixels onto white when producing JPEG outputs.
	draw.Draw(out, out.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	sampled := image.NewNRGBA(out.Bounds())
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sampled.Set(x, y, src.At(bounds.Min.X+x*sw/w, bounds.Min.Y+y*sh/h))
		}
	}
	draw.Draw(out, out.Bounds(), sampled, image.Point{}, draw.Over)
	return out
}
