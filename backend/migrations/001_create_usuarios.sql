CREATE TABLE IF NOT EXISTS usuarios (
    id INT AUTO_INCREMENT PRIMARY KEY,
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    creado_en TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO usuarios (email, password_hash)
VALUES ('test@ticket.com', '$2a$10$A96jsD5Xg6qvXr4NWn96qeq9LFZxcsKQnF5./yNIvcPGiEBIKI0Ye')
ON DUPLICATE KEY UPDATE email = 'test@ticket.com';
