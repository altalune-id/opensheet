// Package db opens the *sql.DB used by every module.
package db

import (
	"fmt"
	"time"

	"github.com/go-playground/validator/v10"
)

// Driver selects the underlying database/sql driver.
type Driver string

const (
	DriverPostgres Driver = "postgres"
	DriverSQLite   Driver = "sqlite"
)

// DBConfig is the driver-agnostic input to Open.
type DBConfig struct {
	Driver          Driver         `yaml:"driver"          mapstructure:"driver"          awareness:"required,bootstrap" validate:"required,oneof=postgres sqlite"`
	DSN             string         `yaml:"dsn"             mapstructure:"dsn"             awareness:"required,secret"    validate:"required"`
	Schema          string         `yaml:"schema"          mapstructure:"schema"          awareness:"bootstrap"`
	TablePrefix     string         `yaml:"tablePrefix"     mapstructure:"tablePrefix"     awareness:"bootstrap"`
	Role            string         `yaml:"role"            mapstructure:"role"            awareness:"bootstrap"`
	Migrator        MigratorConfig `yaml:"migrator"        mapstructure:"migrator"`
	Reader          ReaderConfig   `yaml:"reader"          mapstructure:"reader"`
	Health          HealthConfig   `yaml:"health"          mapstructure:"health"`
	AutoMigrate     bool           `yaml:"autoMigrate"     mapstructure:"autoMigrate"`
	AllowBypassRLS  bool           `yaml:"allowBypassRLS"  mapstructure:"allowBypassRLS"  awareness:"bootstrap"`
	MaxOpenConns    int            `yaml:"maxOpenConns"    mapstructure:"maxOpenConns"                                   validate:"gte=0"`
	MaxIdleConns    int            `yaml:"maxIdleConns"    mapstructure:"maxIdleConns"                                   validate:"gte=0"`
	ConnMaxLifetime time.Duration  `yaml:"connMaxLifetime" mapstructure:"connMaxLifetime"                                validate:"gte=0"`
	ConnMaxIdleTime time.Duration  `yaml:"connMaxIdleTime" mapstructure:"connMaxIdleTime"                                validate:"gte=0"`
	ConnectTimeout  time.Duration  `yaml:"connectTimeout"  mapstructure:"connectTimeout"  awareness:"-"                  validate:"gte=0"`
	ConnectBackoff  time.Duration  `yaml:"connectBackoff"  mapstructure:"connectBackoff"  awareness:"-"                  validate:"gte=0"`
}

// ReaderConfig points reads at a replica pool. When DSN is empty, reads fall back to the writer pool — safe default that matches single-node deploys.
type ReaderConfig struct {
	DSN             string        `yaml:"dsn"             mapstructure:"dsn"             awareness:"secret"`
	Role            string        `yaml:"role"            mapstructure:"role"            awareness:"bootstrap"`
	MaxOpenConns    int           `yaml:"maxOpenConns"    mapstructure:"maxOpenConns"                                   validate:"gte=0"`
	MaxIdleConns    int           `yaml:"maxIdleConns"    mapstructure:"maxIdleConns"                                   validate:"gte=0"`
	ConnMaxLifetime time.Duration `yaml:"connMaxLifetime" mapstructure:"connMaxLifetime"                                validate:"gte=0"`
	ConnMaxIdleTime time.Duration `yaml:"connMaxIdleTime" mapstructure:"connMaxIdleTime"                                validate:"gte=0"`
}

// MigratorConfig holds a separate DB credential used only to run migrations. When Migrator.DSN is set, boot opens a short-lived connection to it, applies migrations, and closes it — the runtime connection then uses DBConfig.DSN as an unprivileged app user.
type MigratorConfig struct {
	DSN  string `yaml:"dsn"  mapstructure:"dsn"  awareness:"secret,bootstrap"`
	Role string `yaml:"role" mapstructure:"role" awareness:"bootstrap"`
}

// HealthConfig tunes the db-health worker.
type HealthConfig struct {
	Interval time.Duration `yaml:"interval" mapstructure:"interval" awareness:"-" validate:"gte=0"`
	Timeout  time.Duration `yaml:"timeout"  mapstructure:"timeout"  awareness:"-" validate:"gte=0"`
}

var v10 = validator.New() //nolint:gochecknoglobals // validator instance is stateless + safe for concurrent use; shared per go-playground/validator conventions.

// Validate rejects obviously-wrong configs before we touch the driver.
func (c DBConfig) Validate() error {
	if err := v10.Struct(c); err != nil {
		return fmt.Errorf("db: %w", err)
	}
	return nil
}
