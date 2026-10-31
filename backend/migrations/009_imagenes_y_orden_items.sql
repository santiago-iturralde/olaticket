-- Fase 4.3: imágenes del evento (Cloudinary) y carrito multi-sector.
-- Una orden ahora puede llevar entradas de varios sectores: cada línea (sector, cantidad,
-- precio congelado) pasa a orden_items y el total de la orden es la suma de sus ítems.

ALTER TABLE eventos
    ADD COLUMN portada_url VARCHAR(500) NULL,
    ADD COLUMN mapa_url VARCHAR(500) NULL;

-- sector_id sin FK, igual que en ordenes: el ítem conserva el historial aunque se borre
-- el sector o el evento. precio_unitario queda NULL hasta que el worker reserva el stock.
CREATE TABLE IF NOT EXISTS orden_items (
    id INT AUTO_INCREMENT PRIMARY KEY,
    orden_id INT NOT NULL,
    sector_id INT NULL,
    cantidad INT NOT NULL,
    precio_unitario DECIMAL(10,2) NULL,
    UNIQUE KEY uq_orden_items_orden_sector (orden_id, sector_id),
    INDEX idx_orden_items_sector (sector_id),
    CONSTRAINT fk_orden_items_orden
        FOREIGN KEY (orden_id) REFERENCES ordenes (id)
        ON UPDATE CASCADE ON DELETE CASCADE
);

-- Cada orden existente pasa a tener un único ítem con su sector, cantidad y precio.
INSERT INTO orden_items (orden_id, sector_id, cantidad, precio_unitario)
SELECT id, sector_id, cantidad, precio_unitario FROM ordenes;

ALTER TABLE ordenes
    DROP INDEX idx_ordenes_sector_estado,
    DROP COLUMN sector_id,
    DROP COLUMN cantidad,
    DROP COLUMN precio_unitario;
