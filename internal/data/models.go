package data

import "database/sql"

type Models struct {
	Images ImageModel
}

func NewModels(db *sql.DB) Models {
	return Models{
		Images: ImageModel{DB: db},
	}
}
