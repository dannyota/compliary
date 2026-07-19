package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
