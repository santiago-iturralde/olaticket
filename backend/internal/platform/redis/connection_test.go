package redis

import (
	"strings"
	"testing"
)

func TestOptionsFromURL(t *testing.T) {
	// Credenciales inventadas: el formato es el de Upstash.
	t.Setenv("REDIS_URL", "rediss://default:clave-secreta@ejemplo-123.upstash.io:6379")
	t.Setenv("REDIS_HOST", "ignorado")

	opts, err := optionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.Addr != "ejemplo-123.upstash.io:6379" || opts.Username != "default" || opts.Password != "clave-secreta" {
		t.Fatalf("opciones = addr %q user %q", opts.Addr, opts.Username)
	}
	if opts.TLSConfig == nil || opts.TLSConfig.ServerName != "ejemplo-123.upstash.io" {
		t.Fatal("rediss:// debe usar TLS con el nombre del host")
	}
	if opts.PoolSize != 100 {
		t.Fatalf("pool = %d", opts.PoolSize)
	}
}

func TestOptionsFromHostPort(t *testing.T) {
	t.Setenv("REDIS_URL", "")
	t.Setenv("REDIS_HOST", "localhost")
	t.Setenv("REDIS_PORT", "6379")

	opts, err := optionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.Addr != "localhost:6379" || opts.Password != "" || opts.TLSConfig != nil {
		t.Fatalf("local: addr %q tls %v", opts.Addr, opts.TLSConfig != nil)
	}
}

func TestInvalidURLDoesNotLeakPassword(t *testing.T) {
	t.Setenv("REDIS_URL", "ftp://default:clave-secreta@host:6379")
	_, err := optionsFromEnv()
	if err == nil {
		t.Fatal("se esperaba error")
	}
	if strings.Contains(err.Error(), "clave-secreta") {
		t.Fatal("el error expone la contraseña")
	}
}
