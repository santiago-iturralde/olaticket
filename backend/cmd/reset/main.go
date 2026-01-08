package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
)

func main() {
	fmt.Println("🏗️  MODO PROFESIONAL: ACTUALIZANDO BASE DE DATOS...")

	dsn := "root:secretpassword@tcp(localhost:3306)/stockdb"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal("❌ Error conectando a MySQL:", err)
	}
	defer db.Close()

	// 1. LIMPIEZA TOTAL
	_, _ = db.Exec("DROP TABLE IF EXISTS ordenes") // Borramos historial viejo
	_, _ = db.Exec("DROP TABLE IF EXISTS eventos")
	_, _ = db.Exec("DROP TABLE IF EXISTS events")
	fmt.Println("🧹 Tablas viejas eliminadas.")

	// 2. CREAR TABLA DE EVENTOS
	queryEventos := `
	CREATE TABLE eventos (
		id INT AUTO_INCREMENT PRIMARY KEY,
		nombre VARCHAR(255) NOT NULL,
		total_tickets INT NOT NULL,
		stock_disponible INT NOT NULL
	);`
	if _, err := db.Exec(queryEventos); err != nil {
		log.Fatal("❌ Error creando tabla eventos:", err)
	}

	// 3. CREAR TABLA DE ÓRDENES
	// Estados posibles: 'pendiente', 'confirmada', 'rechazada'
	queryOrdenes := `
	CREATE TABLE ordenes (
		id INT AUTO_INCREMENT PRIMARY KEY,
		evento_id INT NOT NULL,
		usuario_id INT NOT NULL,
		estado VARCHAR(50) NOT NULL DEFAULT 'pendiente',
		fecha TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(queryOrdenes); err != nil {
		log.Fatal("❌ Error creando tabla ordenes:", err)
	}
	fmt.Println("✅ Tabla 'ordenes' creada para seguimiento.")

	// 4. INSERTAR LOS 3 EVENTOS DE OLAVARRÍA

	// Evento 1: RAC vs Estudiantes (Reemplaza a la Computadora)
	// ID: 1 | 5000 Total | 4950 Disponibles
	queryE1 := `INSERT INTO eventos (id, nombre, total_tickets, stock_disponible) VALUES (1, 'RAC(O) vs Estudiantes(O)', 5000, 4950)`
	if _, err := db.Exec(queryE1); err != nil {
		log.Fatal("❌ Error insertando RAC vs Estudiantes:", err)
	}

	// Evento 2: Argentina vs Venezuela
	// ID: 2 | 60000 Total | 150 Disponibles (¡Poco stock!)
	queryE2 := `INSERT INTO eventos (id, nombre, total_tickets, stock_disponible) VALUES (2, 'Argentina vs Venezuela', 60000, 150)`
	if _, err := db.Exec(queryE2); err != nil {
		log.Fatal("❌ Error insertando Argentina:", err)
	}

	// Evento 3: Bonsifest
	// ID: 3 | 2000 Total | 2000 Disponibles
	queryE3 := `INSERT INTO eventos (id, nombre, total_tickets, stock_disponible) VALUES (3, 'Bonsifest - Edición Verano', 2000, 2000)`
	if _, err := db.Exec(queryE3); err != nil {
		log.Fatal("❌ Error insertando Bonsifest:", err)
	}

	fmt.Println("✅ Eventos creados:")
	fmt.Println("   1. RAC(O) vs Estudiantes(O)")
	fmt.Println("   2. Argentina vs Venezuela")
	fmt.Println("   3. Bonsifest")

	// 5. LIMPIAR REDIS
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	rdb.FlushAll(context.Background())
	fmt.Println("✅ Redis (Colas y Filas) limpiado.")

	fmt.Println("🚀 BASE DE DATOS LISTA CON LOS EVENTOS NUEVOS.")
}
