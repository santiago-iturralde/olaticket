package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewConnection crea una conexión a Redis
func NewConnection() (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379", // Dirección del contenedor (localhost porque corres Go fuera de docker)
		Password: "",               // Sin contraseña por defecto
		DB:       0,                // Base de datos por defecto

		// Configuración del Pool de conexiones de Redis
		PoolSize:     100, // Aguantar muchas conexiones simultáneas
		MinIdleConns: 10,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Hacemos un PING para ver si responde
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("error conectando a redis: %w", err)
	}

	return rdb, nil
}
