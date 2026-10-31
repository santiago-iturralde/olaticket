package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"stock-api/internal/domain"
)

const productoraColumns = `o.id, o.nombre, o.email_contacto, o.telefono, o.creado_en,
	(SELECT COUNT(*) FROM eventos e WHERE e.organizador_id = o.id),
	(SELECT COUNT(*) FROM usuarios u WHERE u.organizador_id = o.id AND u.rol IN ('organizador', 'portero'))`

func scanProductora(row rowScanner) (domain.ProductoraSummary, error) {
	var p domain.ProductoraSummary
	err := row.Scan(&p.ID, &p.Nombre, &p.EmailContacto, &p.Telefono, &p.CreadoEn, &p.Eventos, &p.Usuarios)
	return p, err
}

// ListProductoras lista las productoras con cuántos eventos y usuarios tiene cada una.
func (r *Repository) ListProductoras(ctx context.Context) ([]domain.ProductoraSummary, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+productoraColumns+" FROM organizadores o ORDER BY o.nombre")
	if err != nil {
		return nil, fmt.Errorf("error listing productoras: %w", err)
	}
	defer rows.Close()

	productoras := []domain.ProductoraSummary{}
	for rows.Next() {
		p, err := scanProductora(rows)
		if err != nil {
			return nil, err
		}
		productoras = append(productoras, p)
	}
	return productoras, rows.Err()
}

// GetProductora trae una productora con sus contadores.
func (r *Repository) GetProductora(ctx context.Context, id int64) (*domain.ProductoraSummary, error) {
	p, err := scanProductora(r.db.QueryRowContext(ctx, "SELECT "+productoraColumns+" FROM organizadores o WHERE o.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrOrganizadorNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("error fetching productora: %w", err)
	}
	return &p, nil
}

func (r *Repository) CreateProductora(ctx context.Context, p domain.Organizador) (int64, error) {
	result, err := r.db.ExecContext(
		ctx,
		"INSERT INTO organizadores (nombre, email_contacto, telefono) VALUES (?, ?, ?)",
		p.Nombre, p.EmailContacto, p.Telefono,
	)
	if err != nil {
		return 0, fmt.Errorf("error creating productora: %w", err)
	}
	return result.LastInsertId()
}

func (r *Repository) UpdateProductora(ctx context.Context, p domain.Organizador) error {
	result, err := r.db.ExecContext(
		ctx,
		"UPDATE organizadores SET nombre = ?, email_contacto = ?, telefono = ? WHERE id = ?",
		p.Nombre, p.EmailContacto, p.Telefono, p.ID,
	)
	if err != nil {
		return fmt.Errorf("error updating productora: %w", err)
	}
	// 0 filas también si no cambió nada: confirmamos que exista.
	if n, err := result.RowsAffected(); err == nil && n == 0 {
		if _, err := r.GetProductora(ctx, p.ID); err != nil {
			return err
		}
	}
	return nil
}

// DeleteProductora elimina una productora sin eventos ni usuarios del equipo. Bloquea la
// fila mientras cuenta, así un alta de evento o de usuario en paralelo espera (las FK de
// eventos y usuarios toman un lock compartido sobre la productora).
func (r *Repository) DeleteProductora(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error starting productora delete: %w", err)
	}
	defer tx.Rollback()

	var inUse domain.ProductoraInUseError
	err = tx.QueryRowContext(
		ctx,
		`SELECT (SELECT COUNT(*) FROM eventos e WHERE e.organizador_id = o.id),
		        (SELECT COUNT(*) FROM usuarios u WHERE u.organizador_id = o.id AND u.rol IN ('organizador', 'portero'))
		 FROM organizadores o WHERE o.id = ? FOR UPDATE`,
		id,
	).Scan(&inUse.Eventos, &inUse.Usuarios)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrOrganizadorNotFound
	}
	if err != nil {
		return fmt.Errorf("error locking productora: %w", err)
	}
	if inUse.Eventos > 0 || inUse.Usuarios > 0 {
		return &inUse
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM organizadores WHERE id = ?", id); err != nil {
		return fmt.Errorf("error deleting productora: %w", err)
	}
	return tx.Commit()
}
