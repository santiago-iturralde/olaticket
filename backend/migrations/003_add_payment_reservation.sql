-- Fase 3: precio de eventos, reserva temporal de stock y pagos con Mercado Pago.
ALTER TABLE eventos
    ADD COLUMN precio DECIMAL(10,2) NOT NULL DEFAULT 1000.00;

ALTER TABLE ordenes
    ADD COLUMN cantidad INT NOT NULL DEFAULT 1,
    ADD COLUMN reservada_hasta DATETIME NULL,
    ADD COLUMN payment_id VARCHAR(64) NULL,
    ADD INDEX idx_ordenes_estado_reserva (estado, reservada_hasta);
