package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	mysqlDriver "github.com/go-sql-driver/mysql"

	"stock-api/internal/domain"
)

// maxUsersListed acota el listado del backoffice: con más usuarios se busca con q.
const maxUsersListed = 200

// Roles que gestiona el panel: las cuentas de clientes (rol usuario) no se listan ni se
// modifican desde ahí. Un organizador solo ve a los de su productora (sin admins).
const (
	staffRoles      = "'admin', 'organizador', 'portero'"
	productoraRoles = "'organizador', 'portero'"
)

// ListUsers lista el equipo (los más nuevos primero), filtrando por email, nombre o apellido
// si q no está vacío. scope = 0 (admin) trae a todo el equipo; scope > 0, solo organizadores y
// porteros de esa productora. Retorna también cuántos coinciden en total.
func (r *Repository) ListUsers(ctx context.Context, q string, scope int64) ([]domain.UserSummary, int, error) {
	where := " WHERE u.rol IN (" + staffRoles + ")"
	var args []any
	if scope > 0 {
		where = " WHERE u.rol IN (" + productoraRoles + ") AND u.organizador_id = ?"
		args = append(args, scope)
	}
	if q = strings.TrimSpace(q); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
		where += " AND (u.email LIKE ? OR u.nombre LIKE ? OR u.apellido LIKE ? OR CONCAT(u.nombre, ' ', u.apellido) LIKE ?)"
		args = append(args, like, like, like, like)
	}

	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM usuarios u"+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("error counting users: %w", err)
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT u.id, u.email, u.nombre, u.apellido, u.rol, COALESCE(u.organizador_id, 0), COALESCE(o.nombre, ''), u.creado_en
		 FROM usuarios u
		 LEFT JOIN organizadores o ON o.id = u.organizador_id`+where+`
		 ORDER BY u.id DESC LIMIT ?`,
		append(args, maxUsersListed)...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("error listing users: %w", err)
	}
	defer rows.Close()

	users := []domain.UserSummary{}
	for rows.Next() {
		var u domain.UserSummary
		if err := rows.Scan(&u.ID, &u.Email, &u.Nombre, &u.Apellido, &u.Rol, &u.OrganizadorID, &u.OrganizadorNombre, &u.CreadoEn); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

// lockStaffUser bloquea la fila del usuario dentro de la transacción y confirma que el que
// opera pueda tocarlo. Para el admin (scope 0): ErrUserNotFound si no existe, ErrCustomerAccount
// si es un cliente. Para un organizador (scope > 0) todo lo que no sea un organizador o portero
// de su productora es ErrUserNotFound: no revela cuentas ajenas.
// Al hacerlo en la misma transacción que la escritura, el rol no cambia entre control y cambio.
func lockStaffUser(ctx context.Context, tx *sql.Tx, userID, scope int64) error {
	var rol string
	var organizadorID int64
	err := tx.QueryRowContext(
		ctx,
		"SELECT rol, COALESCE(organizador_id, 0) FROM usuarios WHERE id = ? FOR UPDATE",
		userID,
	).Scan(&rol, &organizadorID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("error locking user: %w", err)
	}
	switch {
	case scope > 0 && (!domain.RolDeProductora(rol) || organizadorID != scope):
		return domain.ErrUserNotFound
	case !domain.RolDeEquipo(rol):
		return domain.ErrCustomerAccount
	}
	return nil
}

// staffWrite corre write en una transacción, después de verificar que el usuario sea del
// equipo y esté dentro del scope del que opera.
func (r *Repository) staffWrite(ctx context.Context, userID, scope int64, write func(tx *sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error starting user change: %w", err)
	}
	defer tx.Rollback()

	if err := lockStaffUser(ctx, tx, userID, scope); err != nil {
		return err
	}
	if err := write(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateUserRole cambia el rol de un usuario del equipo. organizadorID solo se guarda para los
// roles de productora (organizador y portero); para el admin queda en NULL.
func (r *Repository) UpdateUserRole(ctx context.Context, userID int64, rol string, organizadorID, scope int64) error {
	if !domain.RolDeProductora(rol) {
		organizadorID = 0
	}
	return r.staffWrite(ctx, userID, scope, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			ctx,
			"UPDATE usuarios SET rol = ?, organizador_id = NULLIF(?, 0) WHERE id = ?",
			rol, organizadorID, userID,
		)
		if err != nil {
			return userWriteError(err, "error updating user role")
		}
		return nil
	})
}

// GetUserRole lee el rol vigente del usuario (y su productora, si es organizador).
func (r *Repository) GetUserRole(ctx context.Context, userID int64) (string, int64, error) {
	var rol string
	var organizadorID int64
	err := r.db.QueryRowContext(
		ctx,
		"SELECT rol, COALESCE(organizador_id, 0) FROM usuarios WHERE id = ?",
		userID,
	).Scan(&rol, &organizadorID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, domain.ErrUserNotFound
	}
	if err != nil {
		return "", 0, fmt.Errorf("error reading user role: %w", err)
	}
	return rol, organizadorID, nil
}

// CreateUser da de alta un usuario desde el backoffice (sin documento ni datos de perfil:
// el usuario los completa después). El índice UNIQUE de email resuelve los duplicados.
func (r *Repository) CreateUser(ctx context.Context, u domain.NewUser) (int64, error) {
	if !domain.RolDeProductora(u.Rol) {
		u.OrganizadorID = 0
	}
	result, err := r.db.ExecContext(
		ctx,
		`INSERT INTO usuarios (email, password_hash, nombre, apellido, rol, organizador_id)
		 VALUES (?, ?, ?, ?, ?, NULLIF(?, 0))`,
		u.Email, u.PasswordHash, u.Nombre, u.Apellido, u.Rol, u.OrganizadorID,
	)
	if err != nil {
		return 0, userWriteError(err, "error creating user")
	}
	return result.LastInsertId()
}

// UpdateUserProfile cambia nombre, apellido y email de un usuario del equipo.
func (r *Repository) UpdateUserProfile(ctx context.Context, userID, scope int64, nombre, apellido, email string) error {
	return r.staffWrite(ctx, userID, scope, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			ctx,
			"UPDATE usuarios SET nombre = ?, apellido = ?, email = ? WHERE id = ?",
			nombre, apellido, email, userID,
		)
		if err != nil {
			return userWriteError(err, "error updating user")
		}
		return nil
	})
}

// DeleteUser elimina un usuario del equipo sin órdenes. Con órdenes (de cualquier estado)
// retorna *UserHasOrdersError: sus compras y entradas nominativas lo referencian.
func (r *Repository) DeleteUser(ctx context.Context, userID, scope int64) error {
	return r.staffWrite(ctx, userID, scope, func(tx *sql.Tx) error {
		var orders int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM ordenes WHERE usuario_id = ?", userID).Scan(&orders); err != nil {
			return fmt.Errorf("error counting user orders: %w", err)
		}
		if orders > 0 {
			return &domain.UserHasOrdersError{Orders: orders}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM usuarios WHERE id = ?", userID); err != nil {
			return fmt.Errorf("error deleting user: %w", err)
		}
		return nil
	})
}

// GetUserSummary trae un usuario tal como lo muestra la gestión de usuarios.
func (r *Repository) GetUserSummary(ctx context.Context, userID int64) (*domain.UserSummary, error) {
	var u domain.UserSummary
	err := r.db.QueryRowContext(
		ctx,
		`SELECT u.id, u.email, u.nombre, u.apellido, u.rol, COALESCE(u.organizador_id, 0), COALESCE(o.nombre, ''), u.creado_en
		 FROM usuarios u
		 LEFT JOIN organizadores o ON o.id = u.organizador_id
		 WHERE u.id = ?`,
		userID,
	).Scan(&u.ID, &u.Email, &u.Nombre, &u.Apellido, &u.Rol, &u.OrganizadorID, &u.OrganizadorNombre, &u.CreadoEn)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("error fetching user: %w", err)
	}
	return &u, nil
}

// userWriteError traduce los errores de MySQL al escribir usuarios.
func userWriteError(err error, context string) error {
	var mysqlErr *mysqlDriver.MySQLError
	if errors.As(err, &mysqlErr) {
		switch mysqlErr.Number {
		case 1062: // Único índice de usuarios que se escribe acá: email
			return domain.ErrEmailTaken
		case 1452: // FK: la productora no existe
			return domain.ErrOrganizadorNotFound
		}
	}
	return fmt.Errorf("%s: %w", context, err)
}
