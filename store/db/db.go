package db

import (
	"github.com/pkg/errors"

	"github.com/EgoSay/kairos/internal/profile"
	"github.com/EgoSay/kairos/store"
	"github.com/EgoSay/kairos/store/db/mysql"
	"github.com/EgoSay/kairos/store/db/postgres"
	"github.com/EgoSay/kairos/store/db/sqlite"
)

// NewDBDriver creates new db driver based on profile.
func NewDBDriver(profile *profile.Profile) (store.Driver, error) {
	var driver store.Driver
	var err error

	switch profile.Driver {
	case "sqlite":
		driver, err = sqlite.NewDB(profile)
	case "mysql":
		driver, err = mysql.NewDB(profile)
	case "postgres":
		driver, err = postgres.NewDB(profile)
	default:
		return nil, errors.New("unknown db driver")
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to create db driver")
	}
	return driver, nil
}
