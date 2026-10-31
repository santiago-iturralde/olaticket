-- Fase 4, paso 3: el precio queda congelado en la orden al reservar.
-- Hasta ahora se leía siempre eventos.precio, así que editar el precio desde el backoffice
-- cambiaba la recaudación de ventas pasadas y hacía fallar el control de monto del webhook
-- para quien estaba pagando en ese momento.
ALTER TABLE ordenes
    ADD COLUMN precio_unitario DECIMAL(10,2) NULL,
    -- Para las métricas por evento y para bloquear las órdenes de un evento al eliminarlo.
    ADD INDEX idx_ordenes_evento_estado (evento_id, estado);

-- Las órdenes previas toman el precio actual del evento (es el mejor dato disponible).
UPDATE ordenes o
JOIN eventos e ON e.id = o.evento_id
SET o.precio_unitario = e.precio
WHERE o.precio_unitario IS NULL;
