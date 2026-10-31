package mysql

import (
	"strings"
	"testing"

	driver "github.com/go-sql-driver/mysql"
)

func setDBEnv(t *testing.T, host, port, tlsMode string) {
	t.Helper()
	t.Setenv("DB_USER", "usuario.root")
	t.Setenv("DB_PASSWORD", "cl@ve/secreta")
	t.Setenv("DB_HOST", host)
	t.Setenv("DB_PORT", port)
	t.Setenv("DB_NAME", "test")
	t.Setenv("DB_TLS", tlsMode)
}

func parsedDSN(t *testing.T) *driver.Config {
	t.Helper()
	dsn, err := dsnFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("DSN inválido: %v", err)
	}
	return cfg
}

func TestDSNUsesTLSForCloudHost(t *testing.T) {
	setDBEnv(t, "gateway01.us-east-1.prod.aws.tidbcloud.com", "4000", "")
	cfg := parsedDSN(t)
	if cfg.TLSConfig != "true" {
		t.Fatalf("tls = %q, se esperaba true", cfg.TLSConfig)
	}
	if cfg.Addr != "gateway01.us-east-1.prod.aws.tidbcloud.com:4000" || cfg.DBName != "test" || !cfg.ParseTime {
		t.Fatalf("cfg = addr %q db %q parseTime %v", cfg.Addr, cfg.DBName, cfg.ParseTime)
	}
	// La contraseña con caracteres especiales tiene que sobrevivir el escape del DSN.
	if cfg.User != "usuario.root" || cfg.Passwd != "cl@ve/secreta" {
		t.Fatalf("credenciales mal escapadas: user %q", cfg.User)
	}
}

func TestDSNWithoutTLSForLocalHosts(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "db", "192.168.1.20", "::1"} {
		setDBEnv(t, host, "3306", "")
		if cfg := parsedDSN(t); cfg.TLSConfig != "" {
			t.Fatalf("%s: tls = %q, se esperaba sin TLS", host, cfg.TLSConfig)
		}
	}
}

func TestDSNTLSOverride(t *testing.T) {
	setDBEnv(t, "db", "3306", "skip-verify")
	if cfg := parsedDSN(t); cfg.TLSConfig != "skip-verify" {
		t.Fatalf("tls = %q", cfg.TLSConfig)
	}

	setDBEnv(t, "mi-base.tidbcloud.com", "4000", "false")
	if cfg := parsedDSN(t); cfg.TLSConfig != "" {
		t.Fatalf("DB_TLS=false debería desactivar TLS, tls = %q", cfg.TLSConfig)
	}

	setDBEnv(t, "mi-base.tidbcloud.com", "4000", "quizas")
	if _, err := dsnFromEnv(); err == nil || strings.Contains(err.Error(), "secreta") {
		t.Fatalf("se esperaba error sin la contraseña, err = %v", err)
	}
}
