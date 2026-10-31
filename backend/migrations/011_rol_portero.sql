-- Fase 4.4: rol portero (control de acceso en la puerta).
-- Como el organizador, pertenece a una productora (usuarios.organizador_id) y solo puede
-- escanear entradas de los eventos de esa productora. No tiene acceso al backoffice.
ALTER TABLE usuarios
    MODIFY rol ENUM('usuario', 'organizador', 'admin', 'portero') NOT NULL DEFAULT 'usuario';
