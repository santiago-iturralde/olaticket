import { useEffect, useRef, useState } from 'react'
import { FaPlus, FaPen, FaTrashAlt } from 'react-icons/fa'

const API_URL = import.meta.env.VITE_API_URL

// El panel gestiona solo al equipo. Los clientes (rol usuario) se registran solos en la
// tienda: ni se listan ni se crean acá, y el backend rechaza cualquier cambio sobre ellos.
const ROLES = [
  { value: 'admin', label: 'Administrador' },
  { value: 'organizador', label: 'Organizador' },
  { value: 'portero', label: 'Portero' }
]

// Un organizador solo maneja a su equipo: organizadores y porteros de su productora (la
// productora la pone el backend). El admin elige cualquier rol y productora.
const rolesFor = (viewerRol) => (viewerRol === 'admin' ? ROLES : ROLES.filter((r) => r.value !== 'admin'))
const needsProductora = (rol) => rol === 'organizador' || rol === 'portero'
const roleLabel = (rol) => ROLES.find((r) => r.value === rol)?.label ?? rol

const dateFormatter = new Intl.DateTimeFormat('es-AR', { dateStyle: 'short' })

const EMPTY_USER = { nombre: '', apellido: '', email: '', password: '', rol: 'organizador', organizador_id: '' }

// Mismas reglas que el backend (registro y alta desde el panel).
const validateUser = (form, isCreate, pickProductora) => {
  const errors = {}
  if (!form.nombre.trim()) errors.nombre = 'Ingresá el nombre'
  else if (form.nombre.trim().length > 100) errors.nombre = 'Máximo 100 caracteres'
  if (form.apellido.trim().length > 100) errors.apellido = 'Máximo 100 caracteres'
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.email.trim())) errors.email = 'Ingresá un correo electrónico válido'
  if (isCreate) {
    if (form.password.length < 8) errors.password = 'Al menos 8 caracteres'
    else if (form.password.length > 72) errors.password = 'Máximo 72 caracteres'
    else if (!/\p{Lu}/u.test(form.password)) errors.password = 'Al menos una letra mayúscula'
    else if (!/\d/.test(form.password)) errors.password = 'Al menos un número'
    if (pickProductora && needsProductora(form.rol) && !form.organizador_id) errors.organizador_id = 'Elegí la productora'
  }
  return errors
}

const displayName = (user) => `${user.nombre} ${user.apellido}`.trim() || user.email

// Alta (con contraseña y rol) o edición (nombre, apellido y email) de un usuario.
// pickProductora: el admin elige la productora; para un organizador la asigna el backend (la suya).
function UserForm({ user, roles, pickProductora, organizadores, authToken, onUnauthorized, onSaved, onCancel }) {
  const isCreate = !user
  const [form, setForm] = useState(() =>
    isCreate
      ? { ...EMPTY_USER, rol: pickProductora ? 'organizador' : 'portero' }
      : { ...EMPTY_USER, nombre: user.nombre, apellido: user.apellido, email: user.email }
  )
  const [errors, setErrors] = useState({})
  const [formError, setFormError] = useState('')
  const [saving, setSaving] = useState(false)

  const change = (event) => {
    const { name, value } = event.target
    setForm((prev) => ({ ...prev, [name]: value }))
    setErrors((prev) => ({ ...prev, [name]: undefined }))
  }

  const submit = async (event) => {
    event.preventDefault()
    setFormError('')
    const nextErrors = validateUser(form, isCreate, pickProductora)
    setErrors(nextErrors)
    const first = Object.keys(nextErrors)[0]
    if (first) {
      document.getElementById(`user-${first}`)?.focus()
      return
    }

    const body = { nombre: form.nombre, apellido: form.apellido, email: form.email }
    if (isCreate) {
      Object.assign(body, {
        password: form.password,
        rol: form.rol,
        organizador_id: pickProductora && needsProductora(form.rol) ? Number(form.organizador_id) : 0
      })
    }

    setSaving(true)
    try {
      const res = await fetch(`${API_URL}/api/admin/usuarios${isCreate ? '' : `/${user.id}`}`, {
        method: isCreate ? 'POST' : 'PUT',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${authToken}` },
        body: JSON.stringify(body)
      })
      if (res.status === 401) {
        onUnauthorized()
        return
      }
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        if (data.field) {
          setErrors({ [data.field]: data.error })
          document.getElementById(`user-${data.field}`)?.focus()
        } else {
          setFormError(data.error || 'No se pudo guardar el usuario')
        }
        return
      }
      onSaved(data)
    } catch {
      setFormError('No se pudo conectar con el servidor')
    } finally {
      setSaving(false)
    }
  }

  const field = (name, label, props = {}) => (
    <div className="form-field">
      <label htmlFor={`user-${name}`}>{label}</label>
      <input
        id={`user-${name}`}
        name={name}
        value={form[name]}
        onChange={change}
        aria-invalid={errors[name] ? 'true' : undefined}
        aria-describedby={errors[name] ? `user-${name}-error` : props.hint ? `user-${name}-hint` : undefined}
        {...props.input}
      />
      {errors[name] ? (
        <span id={`user-${name}-error`} className="field-error">{errors[name]}</span>
      ) : (
        props.hint && <span id={`user-${name}-hint`} className="form-notice">{props.hint}</span>
      )}
    </div>
  )

  return (
    <form className="user-form" onSubmit={submit} noValidate>
      <h2 id="user-dialog-title">{isCreate ? 'Nuevo usuario' : `Editar a ${displayName(user)}`}</h2>
      <div className="user-form-grid">
        {field('nombre', 'Nombre', { input: { maxLength: 100, autoComplete: 'off', autoFocus: true } })}
        {field('apellido', 'Apellido (opcional)', { input: { maxLength: 100, autoComplete: 'off' } })}
      </div>
      {field('email', 'Correo electrónico', { input: { type: 'email', maxLength: 255, autoComplete: 'off' } })}
      {isCreate && (
        <>
          {field('password', 'Contraseña inicial', {
            input: { type: 'password', maxLength: 72, autoComplete: 'new-password' },
            hint: 'Mínimo 8 caracteres, con una mayúscula y un número. Pasásela al usuario por un canal seguro.'
          })}
          <div className="user-form-grid">
            <div className="form-field">
              <label htmlFor="user-rol">Rol</label>
              <select id="user-rol" name="rol" value={form.rol} onChange={change}>
                {roles.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
              </select>
            </div>
            {pickProductora && needsProductora(form.rol) && (
              <div className="form-field">
                <label htmlFor="user-organizador_id">Productora</label>
                <select
                  id="user-organizador_id"
                  name="organizador_id"
                  value={form.organizador_id}
                  onChange={change}
                  aria-invalid={errors.organizador_id ? 'true' : undefined}
                  aria-describedby={errors.organizador_id ? 'user-organizador_id-error' : undefined}
                >
                  <option value="">Elegí la productora</option>
                  {organizadores.map((org) => <option key={org.id} value={org.id}>{org.nombre}</option>)}
                </select>
                {errors.organizador_id && <span id="user-organizador_id-error" className="field-error">{errors.organizador_id}</span>}
              </div>
            )}
          </div>
          {!pickProductora && <p className="form-notice">Queda en tu productora.</p>}
          {form.rol === 'portero' && (
            <p className="form-notice">El portero solo usa la app de puerta: al iniciar sesión va directo al escáner.</p>
          )}
        </>
      )}
      {!isCreate && <p className="form-notice">El rol se cambia desde la tabla.</p>}

      {formError && <p className="login-error" role="alert">{formError}</p>}
      <div className="confirm-actions">
        <button type="button" className="logout-btn" onClick={onCancel} disabled={saving}>Cancelar</button>
        <button type="submit" className="buy-btn role-save" disabled={saving}>
          {saving ? 'Guardando...' : isCreate ? 'Crear usuario' : 'Guardar cambios'}
        </button>
      </div>
    </form>
  )
}

// Gestión de roles (solo admin). Cada fila guarda por separado: el rol organizador
// necesita además la productora sobre la que va a operar.
function UsersAdmin({ authToken, rol, userID, onUnauthorized }) {
  const roles = rolesFor(rol)
  const pickProductora = rol === 'admin'
  const [users, setUsers] = useState(null)
  const [total, setTotal] = useState(0)
  const [organizadores, setOrganizadores] = useState([])
  const [query, setQuery] = useState('')
  const [search, setSearch] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  // Cambios sin guardar por usuario: { [id]: { rol, organizador_id } }
  const [drafts, setDrafts] = useState({})
  const [saving, setSaving] = useState(null)
  const [rowErrors, setRowErrors] = useState({})

  // Modal abierto: { type: 'create' } | { type: 'edit', user } | { type: 'delete', user }
  const [dialog, setDialog] = useState(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState('')
  const dialogRef = useRef(null)

  useEffect(() => {
    const el = dialogRef.current
    if (dialog && el && !el.open) el.showModal()
  }, [dialog])

  const openDialog = (next) => {
    setNotice('')
    setDeleteError('')
    setDialog(next)
  }

  const closeDialog = () => {
    if (deleting) return
    dialogRef.current?.close() // onClose limpia el estado
  }

  const handleCreated = (user) => {
    setUsers((prev) => [user, ...(prev || [])])
    setTotal((n) => n + 1)
    setNotice(`Se creó a ${user.email}.`)
    dialogRef.current?.close()
  }

  const handleEdited = (user) => {
    setUsers((prev) => prev.map((u) => (u.id === user.id ? user : u)))
    setNotice(`Se actualizaron los datos de ${user.email}.`)
    dialogRef.current?.close()
  }

  const confirmDelete = async () => {
    const user = dialog.user
    setDeleting(true)
    setDeleteError('')
    try {
      const res = await fetch(`${API_URL}/api/admin/usuarios/${user.id}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${authToken}` }
      })
      if (res.status === 401) {
        onUnauthorized()
        return
      }
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || 'No se pudo eliminar el usuario')
      }
      setUsers((prev) => prev.filter((u) => u.id !== user.id))
      setTotal((n) => n - 1)
      setNotice(`Se eliminó a ${user.email}.`)
      setDeleting(false)
      dialogRef.current?.close()
    } catch (err) {
      setDeleteError(err instanceof TypeError ? 'No se pudo conectar con el servidor' : err.message)
      setDeleting(false)
    }
  }

  useEffect(() => {
    const controller = new AbortController()
    const headers = { Authorization: `Bearer ${authToken}` }
    const get = async (path) => {
      const res = await fetch(`${API_URL}${path}`, { headers, signal: controller.signal })
      if (res.status === 401) {
        onUnauthorized()
        return null
      }
      const data = await res.json()
      if (!res.ok) throw new Error(data.error || 'No se pudieron cargar los usuarios')
      return data
    }
    // Las productoras solo las necesita (y las puede leer) el admin.
    Promise.all([
      get(`/api/admin/usuarios?q=${encodeURIComponent(search)}`),
      pickProductora ? get('/api/admin/organizadores') : Promise.resolve([])
    ])
      .then(([usersData, orgsData]) => {
        if (!usersData) return
        setUsers(usersData.usuarios)
        setTotal(usersData.total)
        setOrganizadores(orgsData || [])
        setError('')
      })
      .catch((err) => {
        if (!controller.signal.aborted) setError(err.message)
      })
    return () => controller.abort()
  }, [authToken, onUnauthorized, search, pickProductora])

  const draftOf = (user) => drafts[user.id] ?? { rol: user.rol, organizador_id: user.organizador_id || '' }

  const changeDraft = (user, field, value) => {
    setNotice('')
    setRowErrors((prev) => ({ ...prev, [user.id]: undefined }))
    setDrafts((prev) => ({ ...prev, [user.id]: { ...draftOf(user), [field]: value } }))
  }

  const isDirty = (user) => {
    const draft = draftOf(user)
    return draft.rol !== user.rol || (pickProductora && needsProductora(draft.rol) && Number(draft.organizador_id) !== user.organizador_id)
  }

  const save = async (user) => {
    const draft = draftOf(user)
    if (pickProductora && needsProductora(draft.rol) && !draft.organizador_id) {
      setRowErrors((prev) => ({ ...prev, [user.id]: 'Elegí la productora' }))
      return
    }
    setSaving(user.id)
    try {
      const res = await fetch(`${API_URL}/api/admin/usuarios/${user.id}/rol`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${authToken}` },
        body: JSON.stringify({ rol: draft.rol, organizador_id: pickProductora ? Number(draft.organizador_id) || 0 : 0 })
      })
      if (res.status === 401) {
        onUnauthorized()
        return
      }
      const data = await res.json().catch(() => ({}))
      if (!res.ok) throw new Error(data.error || 'No se pudo cambiar el rol')

      // El organizador no elige productora: el usuario sigue en la misma (la suya).
      const organizador = pickProductora
        ? organizadores.find((org) => org.id === Number(draft.organizador_id))
        : { id: user.organizador_id, nombre: user.organizador_nombre }
      setUsers((prev) =>
        prev.map((u) =>
          u.id === user.id
            ? {
                ...u,
                rol: draft.rol,
                organizador_id: needsProductora(draft.rol) ? organizador?.id ?? 0 : 0,
                organizador_nombre: needsProductora(draft.rol) ? organizador?.nombre ?? '' : ''
              }
            : u
        )
      )
      setDrafts((prev) => {
        const next = { ...prev }
        delete next[user.id]
        return next
      })
      const label = roleLabel(draft.rol).toLowerCase()
      setNotice(`${user.email} ahora es ${label}. Los permisos rigen ya; el botón del panel lo ve al volver a iniciar sesión.`)
    } catch (err) {
      setRowErrors((prev) => ({ ...prev, [user.id]: err instanceof TypeError ? 'No se pudo conectar con el servidor' : err.message }))
    } finally {
      setSaving(null)
    }
  }

  return (
    <section className="admin-section" aria-labelledby="users-title">
      <div className="admin-section-header">
        <div>
          <h2 id="users-title">Usuarios</h2>
          <p className="admin-subtitle">
            {pickProductora
              ? 'Administradores, organizadores y porteros. Las cuentas de clientes no se gestionan desde el panel.'
              : 'Organizadores y porteros de tu productora.'}
          </p>
        </div>
        <div className="admin-section-tools">
          <form
            className="users-search"
            role="search"
            onSubmit={(event) => {
              event.preventDefault()
              setSearch(query.trim())
            }}
          >
            <label htmlFor="users-q" className="sr-only">Buscar usuarios</label>
            <input
              id="users-q"
              type="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Buscar por email o nombre"
            />
            <button type="submit" className="logout-btn">Buscar</button>
          </form>
          <button type="button" className="buy-btn admin-new-btn" onClick={() => openDialog({ type: 'create' })}>
            <FaPlus aria-hidden="true" /> NUEVO USUARIO
          </button>
        </div>
      </div>

      {notice && <p className="form-success" role="status">{notice}</p>}
      {error && <p className="login-error" role="alert">{error}</p>}
      {users === null && !error && <p className="admin-empty">Cargando usuarios...</p>}
      {users?.length === 0 && <p className="admin-empty">No hay usuarios{search ? ` que coincidan con "${search}"` : ''}.</p>}

      {users?.length > 0 && (
        <>
          {total > users.length && (
            <p className="form-notice">Mostrando {users.length} de {total}. Buscá para encontrar al resto.</p>
          )}
          <div className="admin-table-wrapper">
            <table className="admin-table users-table">
              <thead>
                <tr>
                  <th scope="col">Usuario</th>
                  <th scope="col">Alta</th>
                  <th scope="col">Rol{pickProductora ? ' y productora' : ''}</th>
                  <th scope="col">Acciones</th>
                </tr>
              </thead>
              <tbody>
                {users.map((user) => {
                  const draft = draftOf(user)
                  const self = user.id === userID
                  const rowError = rowErrors[user.id]
                  return (
                    <tr key={user.id}>
                      <td className="admin-event-name">
                        {`${user.nombre} ${user.apellido}`.trim() || '—'}
                        {self && <span className="user-self"> (vos)</span>}
                        <small className="admin-event-meta">{user.email}</small>
                      </td>
                      <td data-label="Alta">{dateFormatter.format(new Date(user.creado_en))}</td>
                      <td className="role-cell" data-label={pickProductora ? 'Rol y productora' : 'Rol'}>
                        <div className="role-controls">
                          <label htmlFor={`rol-${user.id}`} className="sr-only">Rol de {user.email}</label>
                          <select
                            id={`rol-${user.id}`}
                            value={draft.rol}
                            onChange={(event) => changeDraft(user, 'rol', event.target.value)}
                            disabled={self || saving === user.id}
                            title={self ? 'No podés cambiar tu propio rol' : undefined}
                          >
                            {roles.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
                          </select>
                          {pickProductora && needsProductora(draft.rol) && (
                            <>
                              <label htmlFor={`org-${user.id}`} className="sr-only">Productora de {user.email}</label>
                              <select
                                id={`org-${user.id}`}
                                value={draft.organizador_id}
                                onChange={(event) => changeDraft(user, 'organizador_id', event.target.value)}
                                disabled={self || saving === user.id}
                                aria-invalid={rowError ? 'true' : undefined}
                                aria-describedby={rowError ? `rol-${user.id}-error` : undefined}
                              >
                                <option value="">Elegí la productora</option>
                                {organizadores.map((org) => <option key={org.id} value={org.id}>{org.nombre}</option>)}
                              </select>
                            </>
                          )}
                        </div>
                        {rowError && <span id={`rol-${user.id}-error`} className="field-error">{rowError}</span>}
                      </td>
                      <td className="actions">
                        <div className="row-actions">
                          {isDirty(user) && (
                            <button type="button" className="buy-btn role-save" onClick={() => save(user)} disabled={saving === user.id}>
                              {saving === user.id ? 'Guardando...' : 'Guardar rol'}
                            </button>
                          )}
                          <button
                            type="button"
                            className="logout-btn admin-edit-btn"
                            onClick={() => openDialog({ type: 'edit', user })}
                            aria-label={`Editar a ${user.email}`}
                          >
                            <FaPen aria-hidden="true" /> Editar
                          </button>
                          <button
                            type="button"
                            className="admin-delete-btn"
                            onClick={() => openDialog({ type: 'delete', user })}
                            disabled={self}
                            aria-label={`Eliminar a ${user.email}`}
                            title={self ? 'No podés eliminar tu propio usuario' : 'Eliminar usuario'}
                          >
                            <FaTrashAlt aria-hidden="true" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </>
      )}

      <dialog
        ref={dialogRef}
        className={`confirm-dialog${dialog?.type === 'delete' ? '' : ' user-dialog'}`}
        aria-labelledby={dialog?.type === 'delete' ? 'user-delete-title' : 'user-dialog-title'}
        onClose={() => setDialog(null)}
        onCancel={(event) => {
          if (deleting) event.preventDefault()
        }}
      >
        {(dialog?.type === 'create' || dialog?.type === 'edit') && (
          <UserForm
            key={dialog.user?.id ?? 'new'}
            user={dialog.user}
            roles={roles}
            pickProductora={pickProductora}
            organizadores={organizadores}
            authToken={authToken}
            onUnauthorized={onUnauthorized}
            onSaved={dialog.type === 'create' ? handleCreated : handleEdited}
            onCancel={closeDialog}
          />
        )}
        {dialog?.type === 'delete' && (
          <>
            <h2 id="user-delete-title">¿Eliminar a {displayName(dialog.user)}?</h2>
            <p>Se borra la cuenta de <b>{dialog.user.email}</b>. Esta acción no se puede deshacer.</p>
            <p className="form-notice">Solo se pueden eliminar usuarios sin compras.</p>
            {deleteError && <p className="login-error" role="alert">{deleteError}</p>}
            <div className="confirm-actions">
              <button type="button" className="logout-btn" onClick={closeDialog} disabled={deleting} autoFocus>
                {deleteError ? 'Cerrar' : 'Cancelar'}
              </button>
              {!deleteError && (
                <button type="button" className="danger-btn" onClick={confirmDelete} disabled={deleting}>
                  {deleting ? 'Eliminando...' : 'Eliminar usuario'}
                </button>
              )}
            </div>
          </>
        )}
      </dialog>
    </section>
  )
}

export default UsersAdmin
