package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Embed engine selection tests ---

func TestEmbedEngineAutoNoToken(t *testing.T) {
	c := Default()
	c.KaggleToken = ""
	if got := c.EmbedEngine(); got != "local" {
		t.Errorf("EmbedEngine() = %q, want local (no token)", got)
	}
}

func TestEmbedEngineAutoWithToken(t *testing.T) {
	c := Default()
	c.KaggleToken = "some-kgat-token"
	if got := c.EmbedEngine(); got != "kaggle" {
		t.Errorf("EmbedEngine() = %q, want kaggle (token set)", got)
	}
}

func TestEmbedEngineExplicitLocal(t *testing.T) {
	c := Default()
	c.KaggleToken = "some-kgat-token" // token set but engine forced local
	c.Embed.Engine = "local"
	if got := c.EmbedEngine(); got != "local" {
		t.Errorf("EmbedEngine() = %q, want local (explicit)", got)
	}
}

func TestEmbedEngineExplicitKaggle(t *testing.T) {
	c := Default()
	c.KaggleToken = ""
	c.Embed.Engine = "kaggle"
	if got := c.EmbedEngine(); got != "kaggle" {
		t.Errorf("EmbedEngine() = %q, want kaggle (explicit)", got)
	}
}

func TestEmbedEnvOverrides(t *testing.T) {
	t.Setenv("KAGGLE_API_TOKEN", "test-token-value")
	t.Setenv("COMPLIARY_EMBED_ENGINE", "kaggle")
	t.Setenv("COMPLIARY_EMBED_KAGGLE_MODEL_DATASET", "user/custom-model")
	c, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.KaggleToken != "test-token-value" {
		t.Errorf("KaggleToken not applied from env")
	}
	if c.Embed.Engine != "kaggle" {
		t.Errorf("Embed.Engine = %q, want kaggle", c.Embed.Engine)
	}
	if c.Embed.Kaggle.ModelDataset != "user/custom-model" {
		t.Errorf("Embed.Kaggle.ModelDataset = %q, want user/custom-model", c.Embed.Kaggle.ModelDataset)
	}
}

func TestEmbedDefaults(t *testing.T) {
	c := Default()
	if c.Embed.Engine != "auto" {
		t.Errorf("Embed.Engine = %q, want auto", c.Embed.Engine)
	}
	if c.Embed.Kaggle.ModelDataset != "danhsoftware/qwen3-embedding-06b-onnx-fp16" {
		t.Errorf("Embed.Kaggle.ModelDataset = %q", c.Embed.Kaggle.ModelDataset)
	}
	if c.Embed.Kaggle.Accelerator != "NvidiaTeslaT4" {
		t.Errorf("Embed.Kaggle.Accelerator = %q", c.Embed.Kaggle.Accelerator)
	}
	if c.Embed.Kaggle.MinBatch != 200 {
		t.Errorf("Embed.Kaggle.MinBatch = %d, want 200", c.Embed.Kaggle.MinBatch)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Database.Port != 10011 || c.Database.DBName != "compliary" {
		t.Errorf("unexpected defaults: %+v", c.Database)
	}
	if c.DataDir != "data" {
		t.Errorf("DataDir = %q, want data", c.DataDir)
	}
}

func TestLoadFileOverridesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "database:\n  host: db.example.com\n  port: 5432\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Database.Host != "db.example.com" || c.Database.Port != 5432 {
		t.Errorf("file not applied: %+v", c.Database)
	}
	if c.Database.User != "compliary" {
		t.Errorf("unset field lost default: %+v", c.Database)
	}
}

func TestEnvPassword(t *testing.T) {
	t.Setenv("COMPLIARY_DATABASE_PASSWORD", "s3cret")
	c, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Database.Password != "s3cret" {
		t.Errorf("password not applied from env")
	}
	if !strings.Contains(c.Database.DSN(), "password=s3cret") {
		t.Errorf("DSN missing password: %s", c.Database.DSN())
	}
	if strings.Contains(c.Database.Redacted(), "s3cret") {
		t.Errorf("Redacted leaks password: %s", c.Database.Redacted())
	}
}

func TestEnvDatabaseOverrides(t *testing.T) {
	t.Setenv("COMPLIARY_DATABASE_HOST", "compliary.abc123.ap-southeast-1.rds.amazonaws.com")
	t.Setenv("COMPLIARY_DATABASE_PORT", "5432")
	t.Setenv("COMPLIARY_DATABASE_USER", "compliary")
	t.Setenv("COMPLIARY_DATABASE_NAME", "compliary")
	t.Setenv("COMPLIARY_DATABASE_SSLMODE", "require")
	c, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Database.Host != "compliary.abc123.ap-southeast-1.rds.amazonaws.com" {
		t.Errorf("Host = %q, not applied from env", c.Database.Host)
	}
	if c.Database.Port != 5432 {
		t.Errorf("Port = %d, want 5432", c.Database.Port)
	}
	if c.Database.User != "compliary" {
		t.Errorf("User = %q, not applied from env", c.Database.User)
	}
	if c.Database.DBName != "compliary" {
		t.Errorf("DBName = %q, not applied from env", c.Database.DBName)
	}
	if c.Database.SSLMode != "require" {
		t.Errorf("SSLMode = %q, want require (RDS)", c.Database.SSLMode)
	}
	if !strings.Contains(c.Database.DSN(), "sslmode=require") {
		t.Errorf("DSN missing sslmode=require: %s", c.Database.DSN())
	}
}

func TestDSNQuoting(t *testing.T) {
	d := DatabaseConfig{Host: "local host", Port: 1, User: "u", DBName: "d", SSLMode: "disable", Password: "p w'd"}
	dsn := d.DSN()
	if !strings.Contains(dsn, `host='local host'`) {
		t.Errorf("host not quoted: %s", dsn)
	}
	if !strings.Contains(dsn, `password='p w\'d'`) {
		t.Errorf("password not quoted: %s", dsn)
	}
}
