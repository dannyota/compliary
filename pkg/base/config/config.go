// Package config loads compliary configuration from YAML. Secrets are supplied
// by the environment so they never live in the file. A missing file is not an
// error: built-in defaults matching the podman dev stack are returned so a
// fresh clone runs without setup.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level compliary configuration.
type Config struct {
	Name     string         `yaml:"name"`
	DataDir  string         `yaml:"data_dir"` // operator-built corpus root scanned by the manifest stage
	Database DatabaseConfig `yaml:"database"`
}

// DatabaseConfig holds PostgreSQL connection settings. Password comes from the
// environment (COMPLIARY_DATABASE_PASSWORD), never the YAML file.
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
	Password string `yaml:"-"`
}

// Default returns the built-in configuration, matching the local podman dev
// stack (deploy/compose/compliary.yaml).
func Default() *Config {
	return &Config{
		Name:    "compliary",
		DataDir: "data",
		Database: DatabaseConfig{
			Host:    "localhost",
			Port:    10011,
			User:    "compliary",
			DBName:  "compliary",
			SSLMode: "disable",
		},
	}
}

// Load reads configuration from path, falling back to Default when the file is
// absent. Secrets are always read from the environment.
func Load(path string) (*Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	case os.IsNotExist(err):
		// keep defaults
	default:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	c.applyEnv()
	return c, nil
}

// applyEnv overlays environment variables: the database password (secret,
// env-only) and optional connection overrides for containers/CI.
func (c *Config) applyEnv() {
	if v := os.Getenv("COMPLIARY_DATABASE_PASSWORD"); v != "" {
		c.Database.Password = v
	}
	if v := os.Getenv("COMPLIARY_DATABASE_HOST"); v != "" {
		c.Database.Host = v
	}
	if v := os.Getenv("COMPLIARY_DATABASE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.Database.Port = p
		}
	}
	if v := os.Getenv("COMPLIARY_DATA_DIR"); v != "" {
		c.DataDir = v
	}
}

// DSN returns a libpq connection string, including the password only if set.
func (d DatabaseConfig) DSN() string {
	parts := []string{
		"host=" + dsnQuote(d.Host),
		"port=" + strconv.Itoa(d.Port),
		"user=" + dsnQuote(d.User),
		"dbname=" + dsnQuote(d.DBName),
		"sslmode=" + dsnQuote(d.SSLMode),
	}
	if d.Password != "" {
		parts = append(parts, "password="+dsnQuote(d.Password))
	}
	return strings.Join(parts, " ")
}

// dsnQuote escapes a libpq keyword/value DSN value. A value that is empty or
// contains spaces, quotes, or backslashes is wrapped in single quotes with
// internal quotes/backslashes backslash-escaped.
func dsnQuote(v string) string {
	if v != "" && !strings.ContainsAny(v, ` '\`) {
		return v
	}
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `'`, `\'`)
	return "'" + v + "'"
}

// Redacted returns a DSN safe for logs (no password).
func (d DatabaseConfig) Redacted() string {
	return fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.DBName, d.SSLMode)
}
