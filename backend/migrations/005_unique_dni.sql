-- Una cuenta por documento. Los usuarios previos a la 004 tienen el documento vacío:
-- se pasan a NULL porque el índice UNIQUE de MySQL no considera duplicados a los NULL
-- (varios '' sí chocarían).
-- Si hay documentos repetidos de verdad, el ADD UNIQUE falla: detectalos antes con
--   SELECT tipo_documento, numero_dni, COUNT(*) FROM usuarios
--   WHERE numero_dni <> '' GROUP BY 1, 2 HAVING COUNT(*) > 1;
ALTER TABLE usuarios
    MODIFY tipo_documento VARCHAR(20) NULL DEFAULT NULL,
    MODIFY numero_dni VARCHAR(20) NULL DEFAULT NULL;

UPDATE usuarios SET tipo_documento = NULL WHERE tipo_documento = '';
UPDATE usuarios SET numero_dni = NULL WHERE numero_dni = '';

ALTER TABLE usuarios
    ADD UNIQUE INDEX uq_usuarios_documento (tipo_documento, numero_dni);
