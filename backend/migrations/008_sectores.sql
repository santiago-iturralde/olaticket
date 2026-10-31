-- Fase 4, paso 4: sectores (zonas) y límite de compra por orden.
-- El precio y el cupo dejan de ser del evento: cada sector (General, Platea VIP, ...) tiene
-- los suyos y el stock se descuenta del sector elegido al comprar.
CREATE TABLE IF NOT EXISTS sectores (
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
);

-- Cada evento existente pasa a tener un único sector 'General' con su precio y su stock actual
-- (incluye lo ya vendido o reservado, que sigue descontado).
INSERT INTO sectores (evento_id, nombre, precio, capacidad, stock_disponible)
SELECT id, 'General', precio, total_tickets, stock_disponible FROM eventos;

ALTER TABLE eventos
    ADD COLUMN limite_tickets_por_orden INT NOT NULL DEFAULT 4;

-- Sin FK: igual que evento_id, la orden conserva el historial aunque se borre el evento.
ALTER TABLE ordenes
    ADD COLUMN sector_id INT NULL AFTER evento_id,
    ADD INDEX idx_ordenes_sector_estado (sector_id, estado);

-- Las órdenes previas quedan en el sector 'General' de su evento (las de eventos ya
-- eliminados quedan en NULL: no tienen stock que devolver).
UPDATE ordenes o
JOIN sectores s ON s.evento_id = o.evento_id
SET o.sector_id = s.id
WHERE o.sector_id IS NULL;

ALTER TABLE eventos
    DROP COLUMN precio,
    DROP COLUMN total_tickets,
    DROP COLUMN stock_disponible;
