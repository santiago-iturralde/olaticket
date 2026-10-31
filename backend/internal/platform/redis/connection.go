package redis

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// optionsFromEnv arma la configuración de Redis:
//   - REDIS_URL (producción, ej. Upstash): "rediss://usuario:clave@host:puerto". El esquema
//     rediss:// activa TLS; ParseURL toma también usuario, contraseña y número de base.
//   - Si no está, REDIS_HOST y REDIS_PORT sin contraseña (desarrollo local y Docker).
func optionsFromEnv() (*redis.Options, error) {
	var opts *redis.Options
	if url := strings.TrimSpace(os.Getenv("REDIS_URL")); url != "" {
		parsed, err := redis.ParseURL(url)
		if err != nil {
			// No incluimos la URL en el error: trae la contraseña.
			return nil, fmt.Errorf("REDIS_URL inválida (formato esperado: rediss://usuario:clave@host:puerto)")
		}
		opts = parsed
	} else {
		opts = &redis.Options{
			Addr: fmt.Sprintf("%s:%s", os.Getenv("REDIS_HOST"), os.Getenv("REDIS_PORT")),
		}
	}

	// Configuración del Pool de conexiones de Redis
	opts.PoolSize = 100 // Aguantar muchas conexiones simultáneas
	opts.MinIdleConns = 10
	return opts, nil
}

// NewConnection crea una conexión a Redis y verifica que responda.
func NewConnection() (*redis.Client, error) {
	opts, err := optionsFromEnv()
	if err != nil {
		return nil, err
	}
	rdb := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Hacemos un PING para ver si responde
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("error conectando a redis en %s: %w", opts.Addr, err)
	}
	if opts.TLSConfig != nil {
		log.Printf("🔒 Redis con TLS en %s", opts.Addr) // Solo host:puerto, nunca la contraseña
	}
	return rdb, nil
}
