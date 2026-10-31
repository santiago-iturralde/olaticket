package mysql

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	driver "github.com/go-sql-driver/mysql"
)

// tlsModes son los valores de tls= que entiende el driver sin registrar un certificado propio.
var tlsModes = map[string]bool{"true": true, "false": true, "skip-verify": true, "preferred": true}

// isLocalHost indica si DB_HOST es una base local: localhost, IPs de loopback o privadas, o un
// nombre sin puntos (servicio de Docker como "db"). Esas no usan TLS; un dominio externo sí.
func isLocalHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified()
	}
	return !strings.Contains(host, ".")
}

// dsnFromEnv arma el DSN desde DB_USER, DB_PASSWORD, DB_HOST, DB_PORT y DB_NAME. Con un host
// externo (ej. TiDB Cloud, *.tidbcloud.com) agrega tls=true: TLS verificando el certificado.
// DB_TLS (true, false, skip-verify, preferred) fuerza el modo si hace falta.
func dsnFromEnv() (string, error) {
	host := strings.TrimSpace(os.Getenv("DB_HOST"))
	port := strings.TrimSpace(os.Getenv("DB_PORT"))
	if port == "" {
		port = "3306"
	}

	cfg := driver.NewConfig()
	cfg.User = os.Getenv("DB_USER")
	cfg.Passwd = os.Getenv("DB_PASSWORD")
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(host, port)
	cfg.DBName = os.Getenv("DB_NAME")
	cfg.ParseTime = true

	switch mode := strings.ToLower(strings.TrimSpace(os.Getenv("DB_TLS"))); {
	case mode != "":
		if !tlsModes[mode] {
			return "", fmt.Errorf("DB_TLS inválido %q (usá true, false, skip-verify o preferred)", mode)
		}
		if mode != "false" {
			cfg.TLSConfig = mode
		}
	case !isLocalHost(host):
		cfg.TLSConfig = "true"
	}

	// FormatDSN escapa la contraseña y los parámetros (no hace falta armar el string a mano).
	return cfg.FormatDSN(), nil
}

// NewConnection devuelve una conexión pool a la base de datos.
func NewConnection() (*sql.DB, error) {
	dsn, err := dsnFromEnv()
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("error opening db: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(3 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("error connecting to db: %w", err)
	}

	if cfg, err := driver.ParseDSN(dsn); err == nil && cfg.TLSConfig != "" {
		log.Printf("🔒 MySQL con TLS (%s) en %s", cfg.TLSConfig, cfg.Addr) // Nunca la contraseña
	}
	return db, nil
}
