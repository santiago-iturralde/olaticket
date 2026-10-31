CREATE TABLE IF NOT EXISTS organizadores (
    id INT AUTO_INCREMENT PRIMARY KEY,
    nombre VARCHAR(150) NOT NULL,
    email_contacto VARCHAR(255) NOT NULL,
    telefono VARCHAR(30) NOT NULL DEFAULT '',
    creado_en TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO organizadores (id, nombre, email_contacto, telefono) VALUES
    (1, 'Olaticket Producciones', 'contacto@olaticket.com', '');

CREATE TABLE IF NOT EXISTS usuarios (
    id INT AUTO_INCREMENT PRIMARY KEY,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    nombre VARCHAR(100) NOT NULL DEFAULT '',
    apellido VARCHAR(100) NOT NULL DEFAULT '',
    pais_residencia VARCHAR(60) NOT NULL DEFAULT '',
    telefono VARCHAR(30) NOT NULL DEFAULT '',
    tipo_documento VARCHAR(20) NULL DEFAULT NULL,
    numero_dni VARCHAR(20) NULL DEFAULT NULL,
    fecha_nacimiento DATE NULL,
    acepta_suscripcion BOOLEAN NOT NULL DEFAULT FALSE,
    rol ENUM('usuario', 'organizador', 'admin', 'portero') NOT NULL DEFAULT 'usuario',
    organizador_id INT NULL,
    creado_en TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE INDEX uq_usuarios_documento (tipo_documento, numero_dni),
    CONSTRAINT fk_usuarios_organizador
        FOREIGN KEY (organizador_id) REFERENCES organizadores (id)
        ON UPDATE CASCADE ON DELETE SET NULL
);

INSERT INTO usuarios (email, password_hash, nombre, apellido, pais_residencia, telefono, tipo_documento, numero_dni, fecha_nacimiento)
VALUES ('test@ticket.com', '$2a$10$A96jsD5Xg6qvXr4NWn96qeq9LFZxcsKQnF5./yNIvcPGiEBIKI0Ye',
        'Usuario', 'Prueba', 'Argentina', '+54 2284 000000', 'DNI', '30000000', '1990-01-01')
ON DUPLICATE KEY UPDATE email = 'test@ticket.com';

CREATE TABLE IF NOT EXISTS eventos (
    id INT AUTO_INCREMENT PRIMARY KEY,
    nombre VARCHAR(100) NOT NULL,
    limite_tickets_por_orden INT NOT NULL DEFAULT 4,
    portada_url VARCHAR(500) NULL, -- Cloudinary
    mapa_url VARCHAR(500) NULL,
    organizador_id INT NOT NULL,
    CONSTRAINT fk_eventos_organizador
        FOREIGN KEY (organizador_id) REFERENCES organizadores (id)
        ON UPDATE CASCADE ON DELETE RESTRICT
);

INSERT INTO eventos (id, nombre, limite_tickets_por_orden, organizador_id) VALUES
    (1, 'RAC(O) vs Estudiantes(O)', 4, 1),
    (2, 'Argentina vs Venezuela', 4, 1),
    (3, 'Bonsifest - Edición Verano', 6, 1);

-- Cada sector tiene su precio y su cupo; el stock se descuenta del sector elegido.
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

INSERT INTO sectores (evento_id, nombre, precio, capacidad, stock_disponible) VALUES
    (1, 'Popular', 8000.00, 4000, 3960),
    (1, 'Platea', 14000.00, 1000, 990),
    (2, 'General', 45000.00, 50000, 100),
    (2, 'Platea VIP', 90000.00, 10000, 50),
    (3, 'General', 15000.00, 1800, 1800),
    (3, 'VIP', 30000.00, 200, 200);

CREATE TABLE IF NOT EXISTS ordenes (
    id INT AUTO_INCREMENT PRIMARY KEY,
    evento_id INT NOT NULL,
    usuario_id INT NOT NULL,
    estado VARCHAR(50) NOT NULL DEFAULT 'pendiente',
    fecha TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reservada_hasta DATETIME NULL,
    payment_id VARCHAR(64) NULL,
    usado_en DATETIME NULL,
    qr_token VARCHAR(512) NULL,
    asistentes JSON NULL, -- [{sector_id, nombre, dni}] cargados en el checkout, hasta el pago
    INDEX idx_ordenes_estado_reserva (estado, reservada_hasta),
    INDEX idx_ordenes_evento_estado (evento_id, estado)
);

-- Líneas del carrito: entradas de cada sector dentro de la orden. El total es la suma.
CREATE TABLE IF NOT EXISTS orden_items (
    id INT AUTO_INCREMENT PRIMARY KEY,
    orden_id INT NOT NULL,
    sector_id INT NULL, -- Sin FK: el ítem conserva el historial aunque se borre el sector
    cantidad INT NOT NULL,
    precio_unitario DECIMAL(10,2) NULL, -- Congelado al reservar
    UNIQUE KEY uq_orden_items_orden_sector (orden_id, sector_id),
    INDEX idx_orden_items_sector (sector_id),
    CONSTRAINT fk_orden_items_orden
        FOREIGN KEY (orden_id) REFERENCES ordenes (id)
        ON UPDATE CASCADE ON DELETE CASCADE
);

-- Entradas nominativas: una por persona, se crean al confirmarse el pago.
CREATE TABLE IF NOT EXISTS tickets (
    id INT AUTO_INCREMENT PRIMARY KEY,
    orden_id INT NOT NULL,
    sector_id INT NULL, -- Sin FK: la entrada conserva el historial aunque se borre el sector
    nombre VARCHAR(100) NOT NULL,
    dni VARCHAR(20) NOT NULL,
    ingresado BOOLEAN NOT NULL DEFAULT FALSE,
    ingresado_en DATETIME NULL,
    INDEX idx_tickets_orden (orden_id),
    CONSTRAINT fk_tickets_orden
        FOREIGN KEY (orden_id) REFERENCES ordenes (id)
        ON UPDATE CASCADE ON DELETE CASCADE
);