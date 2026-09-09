package main

import "net/http"

func (app *application) routes() http.Handler {
	// The method and path together decide which handler receives a request.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthcheck", app.healthcheckHandler)
	mux.HandleFunc("POST /v1/images", app.createImageHandler)
	// Serve the browser's CSS and JavaScript from web/assets.
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("web/assets"))))
	mux.HandleFunc("GET /", app.indexHandler)
	return mux
}
