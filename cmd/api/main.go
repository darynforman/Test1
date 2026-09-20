package main

import (
	"context"
	"database/sql"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "github.com/lib/pq"
	"imagelab/internal/data"
)

type config struct {
	port        int
	env         string
	storageDir  string
	workerDelay time.Duration
	db          struct {
		dsn          string
		maxOpenConns int
		maxIdleConns int
		maxIdleTime  time.Duration
	}
}

type application struct {
	config config
	logger *slog.Logger
	models data.Models
}

func main() {
	var cfg config

	// Command-line flags keep machine-specific settings out of the source code.
	flag.IntVar(&cfg.port, "port", 4000, "API server port")
	flag.StringVar(&cfg.env, "env", "development", "Environment (development|staging|production)")
	flag.StringVar(&cfg.storageDir, "storage-dir", "storage", "Image storage directory")
	flag.StringVar(&cfg.db.dsn, "db-dsn", "", "PostgreSQL DSN")
	flag.IntVar(&cfg.db.maxOpenConns, "db-max-open-conns", 25, "PostgreSQL max open connections")
	flag.IntVar(&cfg.db.maxIdleConns, "db-max-idle-conns", 25, "PostgreSQL max idle connections")
	flag.DurationVar(&cfg.db.maxIdleTime, "db-max-idle-time", 15*time.Minute, "PostgreSQL max connection idle time")

	flag.DurationVar(&cfg.workerDelay, "worker-delay", 0, "Optional processing delay for check-in demonstrations")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Confirm the database works before starting the HTTP server.
	db, err := openDB(cfg)
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
	defer db.Close()
	// MkdirAll is safe when the directory already exists.
	if err := os.MkdirAll(filepath.Join(cfg.storageDir, "originals"), 0750); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}

	logger.Info("database connection pool established")

	app := &application{
		config: cfg,
		logger: logger,
		models: data.NewModels(db),
	}

	// Start one background worker for the whole application, not one per upload.
	// Its context lets us tell it to stop when the HTTP server shuts down.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); app.runWorker(workerCtx) }()
	err = app.serve()
	stopWorker()
	// Wait for the worker to finish stopping before closing the database.
	<-workerDone
	if err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func openDB(cfg config) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.db.dsn)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(cfg.db.maxOpenConns)
	db.SetMaxIdleConns(cfg.db.maxIdleConns)
	db.SetConnMaxIdleTime(cfg.db.maxIdleTime)

	// Avoid waiting forever when PostgreSQL is stopped or misconfigured.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = db.PingContext(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}
