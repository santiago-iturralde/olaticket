-- Registro completo de usuarios. Los usuarios existentes quedan con valores vacíos:
-- la obligatoriedad se valida en POST /api/auth/register.
ALTER TABLE usuarios
    ADD COLUMN nombre VARCHAR(100) NOT NULL DEFAULT '',
    ADD COLUMN apellido VARCHAR(100) NOT NULL DEFAULT '',
    ADD COLUMN pais_residencia VARCHAR(60) NOT NULL DEFAULT '',
    ADD COLUMN telefono VARCHAR(30) NOT NULL DEFAULT '',
    ADD COLUMN tipo_documento VARCHAR(20) NOT NULL DEFAULT '',
    ADD COLUMN numero_dni VARCHAR(20) NOT NULL DEFAULT '',
    ADD COLUMN fecha_nacimiento DATE NULL,
    ADD COLUMN acepta_suscripcion BOOLEAN NOT NULL DEFAULT FALSE;
