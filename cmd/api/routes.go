package main

import "net/http"

func (app *application) routes() http.Handler {
	// The method and path together decide which handler receives a request.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthcheck", app.healthcheckHandler)
	mux.HandleFunc("POST /v1/images", app.createImageHandler)
	// These routes let the browser read job progress and open finished images.
	mux.HandleFunc("GET /v1/jobs/{job_id}", app.showJobHandler)
	mux.HandleFunc("GET /v1/images/{image_id}/variants/{name}", app.showVariantHandler)
	// Serve the browser's CSS and JavaScript from web/assets.
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("web/assets"))))
	mux.HandleFunc("GET /", app.indexHandler)
	return mux
}
