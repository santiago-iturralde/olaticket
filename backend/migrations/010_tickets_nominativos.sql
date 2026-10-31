-- Fase 4.4: entradas nominativas y control de acceso.
-- Cada entrada pagada es una fila de tickets con el nombre y DNI de quien la usa y su propio
-- QR (firmado sobre el ticket_id). ingresado marca que ya pasó por el escáner.

-- Datos de los asistentes que carga el comprador en el checkout ([{sector_id, nombre, dni}]).
-- Se guardan acá mientras la orden espera el pago; al confirmarse se pasan a tickets.
ALTER TABLE ordenes
    ADD COLUMN asistentes JSON NULL;

-- sector_id sin FK, igual que en orden_items: la entrada conserva el historial.
CREATE TABLE IF NOT EXISTS tickets (
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
);

-- Las órdenes ya pagadas reciben una entrada por unidad a nombre del comprador
-- (no se pidieron los asistentes cuando se compraron). Si la orden ya se usó con el QR
-- anterior, sus entradas quedan como ingresadas.
INSERT INTO tickets (orden_id, sector_id, nombre, dni, ingresado, ingresado_en)
WITH RECURSIVE n (k) AS (SELECT 1 UNION ALL SELECT k + 1 FROM n WHERE k < 100)
SELECT i.orden_id, i.sector_id,
       COALESCE(NULLIF(TRIM(CONCAT(u.nombre, ' ', u.apellido)), ''), u.email, 'Titular'),
       COALESCE(u.numero_dni, ''),
       o.usado_en IS NOT NULL, o.usado_en
FROM orden_items i
JOIN ordenes o ON o.id = i.orden_id AND o.estado = 'confirmada'
LEFT JOIN usuarios u ON u.id = o.usuario_id
JOIN n ON n.k <= i.cantidad
ORDER BY i.orden_id, i.id, n.k;
