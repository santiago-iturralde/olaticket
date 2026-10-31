-- Fase 4: multi-tenant. Cada evento pertenece a un organizador (productora/teatro)
-- y los usuarios tienen un rol.
CREATE TABLE IF NOT EXISTS organizadores (
    id INT AUTO_INCREMENT PRIMARY KEY,
    nombre VARCHAR(150) NOT NULL,
    email_contacto VARCHAR(255) NOT NULL,
    telefono VARCHAR(30) NOT NULL DEFAULT '',
    creado_en TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Organizador por defecto: se queda con todos los eventos previos a la fase 4.
INSERT INTO organizadores (id, nombre, email_contacto, telefono)
VALUES (1, 'Olaticket Producciones', 'contacto@olaticket.com', '');

-- La columna arranca NULL para poder asignar los eventos existentes antes de
-- volverla obligatoria y agregar la FK.
ALTER TABLE eventos
    ADD COLUMN organizador_id INT NULL;

UPDATE eventos SET organizador_id = 1 WHERE organizador_id IS NULL;

ALTER TABLE eventos
    MODIFY organizador_id INT NOT NULL,
    ADD CONSTRAINT fk_eventos_organizador
        FOREIGN KEY (organizador_id) REFERENCES organizadores (id)
        ON UPDATE CASCADE ON DELETE RESTRICT;

-- organizador_id indica a qué productora gestiona un usuario con rol 'organizador'
-- (NULL para 'usuario' y 'admin'). Sin esto un organizador podría tocar eventos ajenos.
ALTER TABLE usuarios
    ADD COLUMN rol ENUM('usuario', 'organizador', 'admin') NOT NULL DEFAULT 'usuario',
    ADD COLUMN organizador_id INT NULL,
    ADD CONSTRAINT fk_usuarios_organizador
        FOREIGN KEY (organizador_id) REFERENCES organizadores (id)
        ON UPDATE CASCADE ON DELETE SET NULL;
