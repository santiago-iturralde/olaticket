package main

import (
	"context"
	"fmt"
	"log"

	redisClient "stock-api/internal/platform/redis"
	"stock-api/internal/platform/storage/mysql"
)

func main() {
	fmt.Println("🏗️  MODO PROFESIONAL: ACTUALIZANDO BASE DE DATOS...")

	db, err := mysql.NewConnection()
	if err != nil {
		log.Fatal("❌ Error conectando a MySQL:", err)
	}
	defer db.Close()

	// 1. LIMPIEZA TOTAL
	_, _ = db.Exec("DROP TABLE IF EXISTS tickets")
	_, _ = db.Exec("DROP TABLE IF EXISTS orden_items")
	_, _ = db.Exec("DROP TABLE IF EXISTS ordenes") // Borramos historial viejo
	_, _ = db.Exec("DROP TABLE IF EXISTS sectores")
	_, _ = db.Exec("DROP TABLE IF EXISTS eventos")
	_, _ = db.Exec("DROP TABLE IF EXISTS events")
	fmt.Println("🧹 Tablas viejas eliminadas.")

	// 2. ORGANIZADORES: se conservan (no dependen de eventos); solo se asegura el default.
	queryOrganizadores := `
	CREATE TABLE IF NOT EXISTS organizadores (
		id INT AUTO_INCREMENT PRIMARY KEY,
		nombre VARCHAR(150) NOT NULL,
		email_contacto VARCHAR(255) NOT NULL,
		telefono VARCHAR(30) NOT NULL DEFAULT '',
		creado_en TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(queryOrganizadores); err != nil {
		log.Fatal("❌ Error creando tabla organizadores:", err)
	}
	queryDefaultOrganizador := `INSERT IGNORE INTO organizadores (id, nombre, email_contacto) VALUES (1, 'Olaticket Producciones', 'contacto@olaticket.com')`
	if _, err := db.Exec(queryDefaultOrganizador); err != nil {
		log.Fatal("❌ Error insertando organizador por defecto:", err)
	}

	// 3. CREAR TABLA DE EVENTOS
	queryEventos := `
	CREATE TABLE eventos (
		id INT AUTO_INCREMENT PRIMARY KEY,
		nombre VARCHAR(255) NOT NULL,
		limite_tickets_por_orden INT NOT NULL DEFAULT 4,
		portada_url VARCHAR(500) NULL,
		mapa_url VARCHAR(500) NULL,
		organizador_id INT NOT NULL,
		CONSTRAINT fk_eventos_organizador
			FOREIGN KEY (organizador_id) REFERENCES organizadores (id)
			ON UPDATE CASCADE ON DELETE RESTRICT
	);`
	if _, err := db.Exec(queryEventos); err != nil {
		log.Fatal("❌ Error creando tabla eventos:", err)
	}

	// 3b. SECTORES: cada uno con su precio y su cupo; el stock se descuenta del sector elegido.
	querySectores := `
	CREATE TABLE sectores (
		id INT AUTO_INCREMENT PRIMARY KEY,
		evento_id INT NOT NULL,
		nombre VARCHAR(60) NOT NULL,
		precio DECIMAL(10,2) NOT NULL,
		capacidad INT NOT NULL,
		stock_disponible INT NOT NULL,
		INDEX idx_sectores_evento (evento_id),
		CONSTRAINT fk_sectores_evento
			FOREIGN KEY (evento_id) REFERENCES eventos (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);`
	if _, err := db.Exec(querySectores); err != nil {
		log.Fatal("❌ Error creando tabla sectores:", err)
	}

	// 4. CREAR TABLA DE ÓRDENES
	// Estados posibles: 'pendiente', 'reservada', 'confirmada', 'rechazada', 'expirada'
	queryOrdenes := `
	CREATE TABLE ordenes (
		id INT AUTO_INCREMENT PRIMARY KEY,
		evento_id INT NOT NULL,
		usuario_id INT NOT NULL,
		estado VARCHAR(50) NOT NULL DEFAULT 'pendiente',
		fecha TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		reservada_hasta DATETIME NULL,
		payment_id VARCHAR(64) NULL,
		usado_en DATETIME NULL,
		qr_token VARCHAR(512) NULL,
		asistentes JSON NULL,
		INDEX idx_ordenes_estado_reserva (estado, reservada_hasta),
		INDEX idx_ordenes_evento_estado (evento_id, estado)
	);`
	if _, err := db.Exec(queryOrdenes); err != nil {
		log.Fatal("❌ Error creando tabla ordenes:", err)
	}

	// 4b. ÍTEMS DE LA ORDEN: entradas de cada sector del carrito (el total es la suma)
	queryOrdenItems := `
	CREATE TABLE orden_items (
		id INT AUTO_INCREMENT PRIMARY KEY,
		orden_id INT NOT NULL,
		sector_id INT NULL,
		cantidad INT NOT NULL,
		precio_unitario DECIMAL(10,2) NULL, -- Congelado al reservar
		UNIQUE KEY uq_orden_items_orden_sector (orden_id, sector_id),
		INDEX idx_orden_items_sector (sector_id),
		CONSTRAINT fk_orden_items_orden
			FOREIGN KEY (orden_id) REFERENCES ordenes (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);`
	if _, err := db.Exec(queryOrdenItems); err != nil {
		log.Fatal("❌ Error creando tabla orden_items:", err)
	}

	// 4c. ENTRADAS NOMINATIVAS: una por persona, con su propio QR
	queryTickets := `
	CREATE TABLE tickets (
		id INT AUTO_INCREMENT PRIMARY KEY,
		orden_id INT NOT NULL,
		sector_id INT NULL,
		nombre VARCHAR(100) NOT NULL,
		dni VARCHAR(20) NOT NULL,
		ingresado BOOLEAN NOT NULL DEFAULT FALSE,
		ingresado_en DATETIME NULL,
		INDEX idx_tickets_orden (orden_id),
		CONSTRAINT fk_tickets_orden
			FOREIGN KEY (orden_id) REFERENCES ordenes (id)
			ON UPDATE CASCADE ON DELETE CASCADE
	);`
	if _, err := db.Exec(queryTickets); err != nil {
		log.Fatal("❌ Error creando tabla tickets:", err)
	}
	fmt.Println("✅ Tabla 'ordenes' creada para seguimiento.")

	// 5. INSERTAR LOS 3 EVENTOS DE OLAVARRÍA CON SUS SECTORES (mismos datos que init.sql)
	queryEventosSeed := `INSERT INTO eventos (id, nombre, limite_tickets_por_orden, organizador_id) VALUES
		(1, 'RAC(O) vs Estudiantes(O)', 4, 1),
		(2, 'Argentina vs Venezuela', 4, 1),
		(3, 'Bonsifest - Edición Verano', 6, 1)`
	if _, err := db.Exec(queryEventosSeed); err != nil {
		log.Fatal("❌ Error insertando eventos:", err)
	}

	// Argentina vs Venezuela arranca con poco stock para probar el agotado.
	querySectoresSeed := `INSERT INTO sectores (evento_id, nombre, precio, capacidad, stock_disponible) VALUES
		(1, 'Popular', 8000.00, 4000, 3960),
		(1, 'Platea', 14000.00, 1000, 990),
		(2, 'General', 45000.00, 50000, 100),
		(2, 'Platea VIP', 90000.00, 10000, 50),
		(3, 'General', 15000.00, 1800, 1800),
		(3, 'VIP', 30000.00, 200, 200)`
	if _, err := db.Exec(querySectoresSeed); err != nil {
		log.Fatal("❌ Error insertando sectores:", err)
	}

	fmt.Println("✅ Eventos creados:")
	fmt.Println("   1. RAC(O) vs Estudiantes(O)")
	fmt.Println("   2. Argentina vs Venezuela")
	fmt.Println("   3. Bonsifest")

	// 6. LIMPIAR REDIS
	rdb, err := redisClient.NewConnection()
	if err != nil {
		log.Fatal("❌ Error conectando a Redis:", err)
	}
	defer rdb.Close()
	rdb.FlushAll(context.Background())
	fmt.Println("✅ Redis (Colas y Filas) limpiado.")

	fmt.Println("🚀 BASE DE DATOS LISTA CON LOS EVENTOS NUEVOS.")
}
