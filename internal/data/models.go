package data

import "database/sql"

type Models struct {
	Images ImageModel
	// Jobs gives the handlers and worker access to the job database functions.
	Jobs   JobModel
}

// NewModels gives each data model access to the shared database connection pool.
func NewModels(db *sql.DB) Models {
	return Models{
		Images: ImageModel{DB: db},
		Jobs:   JobModel{DB: db},
	}
}
