.DEFAULT_GOAL := help

DB_NAME ?= imagelab
DB_USER ?= $(shell id -un)
IMAGELAB_DB_DSN ?= host=/var/run/postgresql dbname=$(DB_NAME) user=$(DB_USER) sslmode=disable
PORT ?= 4000
WORKER_DELAY ?= 0s
export IMAGELAB_DB_DSN
export PORT WORKER_DELAY DB_NAME

.PHONY: help run db-create db-shell db-tables db-init check-dsn test vet

help:
	@printf '%s\n' \
	  'make run        Start the app at http://localhost:4000 (PORT overrides this)' \
	  'make db-create  Create the database (default: imagelab; uses PostgreSQL environment settings)' \
	  'make db-shell   Open a PostgreSQL prompt' \
	  'make db-tables  List database tables' \
	  'make db-init    Apply all migrations to a FRESH database only' \
	  'make test       Run Go tests' \
	  'make vet        Run Go static checks' \
	  '' \
	  'Defaults to local PostgreSQL, database imagelab, and your Linux username.' \
	  'To use a different connection, override IMAGELAB_DB_DSN:' \
	  '  export IMAGELAB_DB_DSN="postgres://USER:PASSWORD@localhost/imagelab?sslmode=disable"' \
	  'For a slower worker: make run WORKER_DELAY=5s'

check-dsn:
	@test -n "$$IMAGELAB_DB_DSN" || { echo 'Set IMAGELAB_DB_DSN first (see make help).' >&2; exit 1; }

run: check-dsn
	@go run ./cmd/api -db-dsn="$$IMAGELAB_DB_DSN" -port="$$PORT" -worker-delay="$$WORKER_DELAY"

db-create:
	@createdb "$$DB_NAME"

db-shell: check-dsn
	@psql "$$IMAGELAB_DB_DSN"

db-tables: check-dsn
	@psql "$$IMAGELAB_DB_DSN" -X -c '\dt'

# One transaction prevents a failed migration from leaving a partial schema.
# Existing databases already containing these tables do not need this target.
db-init: check-dsn
	@set --; for migration in migrations/*.up.sql; do \
	  set -- "$$@" -f "$$migration"; \
	done; \
	psql "$$IMAGELAB_DB_DSN" -X -v ON_ERROR_STOP=1 --single-transaction "$$@"

test:
	go test ./...

vet:
	go vet ./...
