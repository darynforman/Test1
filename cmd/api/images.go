package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"imagelab/internal/data"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const maxImageBytes = 10 << 20

func (app *application) createImageHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImageBytes+(1<<20))
	file, header, err := r.FormFile("image")
	if err != nil {
		app.badRequestResponse(w, r, fmt.Errorf("image file is required"))
		return
	}
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, maxImageBytes+1))
	if err != nil || len(b) == 0 {
		app.badRequestResponse(w, r, fmt.Errorf("image could not be read"))
		return
	}
	if len(b) > maxImageBytes {
		app.badRequestResponse(w, r, fmt.Errorf("image must not exceed 10 MB"))
		return
	}
	media := http.DetectContentType(b)
	if media != "image/jpeg" && media != "image/png" {
		app.badRequestResponse(w, r, fmt.Errorf("image must be JPEG or PNG"))
		return
	}
	if _, _, err = image.DecodeConfig(bytes.NewReader(b)); err != nil {
		app.badRequestResponse(w, r, fmt.Errorf("image is not a valid JPEG or PNG"))
		return
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}
	ext := ".jpg"
	if media == "image/png" {
		ext = ".png"
	}
	stored := hex.EncodeToString(token) + ext
	path := filepath.Join(app.config.storageDir, "originals", stored)
	if err = os.WriteFile(path, b, 0640); err != nil {
		app.serverErrorResponse(w, r, err)
		return
	}
	img := &data.Image{OriginalFilename: filepath.Base(header.Filename), StoredFilename: stored, MediaType: media, SizeBytes: int64(len(b))}
	err = app.models.Images.Insert(img)
	if err != nil {
		_ = os.Remove(path)
		app.serverErrorResponse(w, r, err)
		return
	}
	if err = app.writeJSON(w, http.StatusCreated, envelope{"image": img, "message": "original image stored; queued jobs are added in Phase 2"}, nil); err != nil {
		app.serverErrorResponse(w, r, err)
	}
}

func (app *application) indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		app.notFoundResponse(w, r)
		return
	}
	http.ServeFile(w, r, "web/index.html")
}
