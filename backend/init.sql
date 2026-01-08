CREATE TABLE IF NOT EXISTS eventos (
    id INT AUTO_INCREMENT PRIMARY KEY,
    nombre VARCHAR(100) NOT NULL,       -- Faltaba coma
    total_tickets INT NOT NULL,         -- Faltaba coma
    stock_disponible INT NOT NULL
);

INSERT INTO eventos (nombre, total_tickets, stock_disponible) VALUES ('Computadora', 2000, 1000);