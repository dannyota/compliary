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
	Embed    EmbedConfig    `yaml:"embed"`

	// KaggleToken is the Kaggle API token (KGAT). Like the DB password it is a
	// secret sourced from the environment (KAGGLE_API_TOKEN), never the YAML file.
	// It drives the "auto" bulk-engine choice and authenticates the Kaggle client.
	KaggleToken string `yaml:"-"`
}

// DatabaseConfig holds PostgreSQL connection settings. Password comes from the
// environment (COMPLIARY_DATABASE_PASSWORD), never the YAML file. Host, port,
// user, dbname, and sslmode can also be overridden from the environment
// (COMPLIARY_DATABASE_HOST/PORT/USER/NAME/SSLMODE) so the deployed container is
// configured entirely by env, with no config file baked into the image.
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
	Password string `yaml:"-"`
}

// EmbedConfig selects how chunk embeddings are produced for indexing. Engine
// only chooses the BULK embedding engine, never the synchronous query path.
//
// Engine: "auto" (default) uses Kaggle when KAGGLE_API_TOKEN is set AND the
// missing-chunk count >= MinBatch, else local ONNX; "local" forces the local
// ONNX embedder; "kaggle" forces the Kaggle batch engine.
type EmbedConfig struct {
	Engine string            `yaml:"engine"` // "auto" | "local" | "kaggle"
	Kaggle EmbedKaggleConfig `yaml:"kaggle"`
}

// EmbedKaggleConfig configures the Kaggle batch embedding engine
// (pkg/rag/embed/kagglebatch). Auth is the KAGGLE_API_TOKEN environment
// variable, never the YAML file.
type EmbedKaggleConfig struct {
	// Owner is the Kaggle username owning the input dataset and embed kernel.
	// Optional: auto-derived from the token when empty.
	Owner string `yaml:"owner"`
	// ModelDataset mounts the Qwen3-Embedding-0.6B ONNX FP16 model from a Kaggle
	// dataset ("owner/slug") so the kernel runs offline.
	ModelDataset string `yaml:"model_dataset"`
	// Accelerator is the Kaggle machine shape, e.g. "NvidiaTeslaT4".
	Accelerator string `yaml:"accelerator"`
	// MinBatch falls back to the local embedder when fewer than this many chunks
	// need embedding (a Kaggle round-trip is not worth it for small batches).
	MinBatch int `yaml:"min_batch"`
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
		Embed: EmbedConfig{
			Engine: "auto",
			Kaggle: EmbedKaggleConfig{
				ModelDataset: "danhsoftware/qwen3-embedding-06b-onnx-fp16",
				Accelerator:  "NvidiaTeslaT4",
				MinBatch:     200,
			},
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
	if v := os.Getenv("COMPLIARY_DATABASE_USER"); v != "" {
		c.Database.User = v
	}
	if v := os.Getenv("COMPLIARY_DATABASE_NAME"); v != "" {
		c.Database.DBName = v
	}
	// SSLMode override matters for managed Postgres: the built-in default is
	// "disable" for the local podman stack, but RDS should run "require".
	if v := os.Getenv("COMPLIARY_DATABASE_SSLMODE"); v != "" {
		c.Database.SSLMode = v
	}
	if v := os.Getenv("COMPLIARY_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("KAGGLE_API_TOKEN"); v != "" {
		c.KaggleToken = v
	}
	if v := os.Getenv("COMPLIARY_EMBED_ENGINE"); v != "" {
		c.Embed.Engine = v
	}
	if v := os.Getenv("COMPLIARY_EMBED_KAGGLE_MODEL_DATASET"); v != "" {
		c.Embed.Kaggle.ModelDataset = v
	}
}

// EmbedEngine resolves the bulk-embedding engine: "kaggle" or "local".
// Configured "auto" (or empty) resolves to "kaggle" when KAGGLE_API_TOKEN is
// set, otherwise "local".
func (c *Config) EmbedEngine() string {
	switch strings.ToLower(strings.TrimSpace(c.Embed.Engine)) {
	case "local":
		return "local"
	case "kaggle":
		return "kaggle"
	default: // "auto" or empty
		if c.KaggleToken != "" {
			return "kaggle"
		}
		return "local"
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
