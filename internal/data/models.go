package data

import "database/sql"

type Models struct {
	Images ImageModel
}

// NewModels gives each data model access to the shared database connection pool.
func NewModels(db *sql.DB) Models {
	return Models{
		Images: ImageModel{DB: db},
	}
}
