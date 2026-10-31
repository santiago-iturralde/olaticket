import { useEffect, useRef, useState } from 'react'
import { FaPlus, FaPen, FaTrashAlt } from 'react-icons/fa'

const API_URL = import.meta.env.VITE_API_URL

const numberFormatter = new Intl.NumberFormat('es-AR')

const EMPTY = { nombre: '', email_contacto: '', telefono: '' }

// Mismas reglas que el backend (organizadores.nombre 150, email 255, teléfono 30).
const validate = (form) => {
  const errors = {}
  if (!form.nombre.trim()) errors.nombre = 'Ingresá el nombre'
  else if (form.nombre.trim().length > 150) errors.nombre = 'Máximo 150 caracteres'
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.email_contacto.trim())) errors.email_contacto = 'Ingresá un correo válido'
  if (form.telefono.trim().length > 30) errors.telefono = 'Máximo 30 caracteres'
  return errors
}

// Alta o edición de una productora.
function ProductoraForm({ productora, authToken, onUnauthorized, onSaved, onCancel }) {
  const isCreate = !productora
  const [form, setForm] = useState(() =>
    isCreate ? EMPTY : { nombre: productora.nombre, email_contacto: productora.email_contacto, telefono: productora.telefono }
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
    const nextErrors = validate(form)
    setErrors(nextErrors)
    const first = Object.keys(nextErrors)[0]
    if (first) {
      document.getElementById(`prod-${first}`)?.focus()
      return
    }

    setSaving(true)
    try {
      const res = await fetch(`${API_URL}/api/admin/productoras${isCreate ? '' : `/${productora.id}`}`, {
        method: isCreate ? 'POST' : 'PUT',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${authToken}` },
        body: JSON.stringify(form)
      })
      if (res.status === 401) {
        onUnauthorized()
        return
      }
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        if (data.field) setErrors({ [data.field]: data.error })
        else setFormError(data.error || 'No se pudo guardar la productora')
        return
      }
      onSaved(data)
    } catch {
      setFormError('No se pudo conectar con el servidor')
    } finally {
      setSaving(false)
    }
  }

  const field = (name, label, input = {}) => (
    <div className="form-field">
      <label htmlFor={`prod-${name}`}>{label}</label>
      <input
        id={`prod-${name}`}
        name={name}
        value={form[name]}
        onChange={change}
        aria-invalid={errors[name] ? 'true' : undefined}
        aria-describedby={errors[name] ? `prod-${name}-error` : undefined}
        {...input}
      />
      {errors[name] && <span id={`prod-${name}-error`} className="field-error">{errors[name]}</span>}
    </div>
  )

  return (
    <form className="user-form" onSubmit={submit} noValidate>
      <h2 id="prod-dialog-title">{isCreate ? 'Nueva productora' : `Editar ${productora.nombre}`}</h2>
      {field('nombre', 'Nombre', { maxLength: 150, autoComplete: 'organization', autoFocus: true })}
      <div className="user-form-grid">
        {field('email_contacto', 'Correo de contacto', { type: 'email', maxLength: 255, autoComplete: 'off' })}
        {field('telefono', 'Teléfono (opcional)', { type: 'tel', maxLength: 30, autoComplete: 'off' })}
      </div>
      {formError && <p className="login-error" role="alert">{formError}</p>}
      <div className="confirm-actions">
        <button type="button" className="logout-btn" onClick={onCancel} disabled={saving}>Cancelar</button>
        <button type="submit" className="buy-btn role-save" disabled={saving}>
          {saving ? 'Guardando...' : isCreate ? 'Crear productora' : 'Guardar cambios'}
        </button>
      </div>
    </form>
  )
}

// CRUD de productoras (solo admin). Una productora con eventos o usuarios no se puede
// eliminar: el backend responde 409 y el motivo se muestra en el diálogo.
function ProductorasAdmin({ authToken, onUnauthorized }) {
  const [productoras, setProductoras] = useState(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  // { type: 'create' } | { type: 'edit', productora } | { type: 'delete', productora }
  const [dialog, setDialog] = useState(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState('')
  const dialogRef = useRef(null)

  useEffect(() => {
    const controller = new AbortController()
    fetch(`${API_URL}/api/admin/productoras`, {
      headers: { Authorization: `Bearer ${authToken}` },
      signal: controller.signal
    })
      .then(async (res) => {
        if (res.status === 401) {
          onUnauthorized()
          return
        }
        const data = await res.json()
        if (!res.ok) throw new Error(data.error || 'No se pudieron cargar las productoras')
        setProductoras(data)
      })
      .catch((err) => {
        if (!controller.signal.aborted) setError(err.message)
      })
    return () => controller.abort()
  }, [authToken, onUnauthorized])

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
    dialogRef.current?.close()
  }

  const handleSaved = (saved) => {
    setProductoras((prev) => {
      const exists = prev.some((p) => p.id === saved.id)
      const next = exists ? prev.map((p) => (p.id === saved.id ? saved : p)) : [...prev, saved]
      return next.sort((a, b) => a.nombre.localeCompare(b.nombre, 'es'))
    })
    setNotice(dialog?.type === 'create' ? `Se creó "${saved.nombre}".` : `Se actualizó "${saved.nombre}".`)
    dialogRef.current?.close()
  }

  const confirmDelete = async () => {
    const { productora } = dialog
    setDeleting(true)
    setDeleteError('')
    try {
      const res = await fetch(`${API_URL}/api/admin/productoras/${productora.id}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${authToken}` }
      })
      if (res.status === 401) {
        onUnauthorized()
        return
      }
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || 'No se pudo eliminar la productora')
      }
      setProductoras((prev) => prev.filter((p) => p.id !== productora.id))
      setNotice(`Se eliminó "${productora.nombre}".`)
      setDeleting(false)
      dialogRef.current?.close()
    } catch (err) {
      setDeleteError(err instanceof TypeError ? 'No se pudo conectar con el servidor' : err.message)
      setDeleting(false)
    }
  }

  return (
    <section className="admin-section" aria-labelledby="productoras-title">
      <div className="admin-section-header">
        <h2 id="productoras-title">Productoras</h2>
        <button type="button" className="buy-btn admin-new-btn" onClick={() => openDialog({ type: 'create' })}>
          <FaPlus aria-hidden="true" /> NUEVA PRODUCTORA
        </button>
      </div>

      {notice && <p className="form-success" role="status">{notice}</p>}
      {error && <p className="login-error" role="alert">{error}</p>}
      {productoras === null && !error && <p className="admin-empty">Cargando productoras...</p>}
      {productoras?.length === 0 && <p className="admin-empty">Todavía no hay productoras.</p>}

      {productoras?.length > 0 && (
        <div className="admin-table-wrapper">
          <table className="admin-table">
            <thead>
              <tr>
                <th scope="col">Productora</th>
                <th scope="col">Teléfono</th>
                <th scope="col" className="num">Eventos</th>
                <th scope="col" className="num">Usuarios</th>
                <th scope="col">Acciones</th>
              </tr>
            </thead>
            <tbody>
              {productoras.map((p) => {
                const inUse = p.eventos > 0 || p.usuarios > 0
                return (
                  <tr key={p.id}>
                    <td className="admin-event-name">
                      {p.nombre}
                      <small className="admin-event-meta">{p.email_contacto}</small>
                    </td>
                    <td data-label="Teléfono">{p.telefono || '—'}</td>
                    <td className="num" data-label="Eventos">{numberFormatter.format(p.eventos)}</td>
                    <td className="num" data-label="Usuarios">{numberFormatter.format(p.usuarios)}</td>
                    <td className="actions">
                      <div className="row-actions">
                        <button
                          type="button"
                          className="logout-btn admin-edit-btn"
                          onClick={() => openDialog({ type: 'edit', productora: p })}
                          aria-label={`Editar ${p.nombre}`}
                        >
                          <FaPen aria-hidden="true" /> Editar
                        </button>
                        <button
                          type="button"
                          className="admin-delete-btn"
                          onClick={() => openDialog({ type: 'delete', productora: p })}
                          aria-label={`Eliminar ${p.nombre}`}
                          title={inUse ? 'Tiene eventos o usuarios: no se puede eliminar' : 'Eliminar productora'}
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
      )}

      <dialog
        ref={dialogRef}
        className={`confirm-dialog${dialog?.type === 'delete' ? '' : ' user-dialog'}`}
        aria-labelledby={dialog?.type === 'delete' ? 'prod-delete-title' : 'prod-dialog-title'}
        onClose={() => setDialog(null)}
        onCancel={(event) => {
          if (deleting) event.preventDefault()
        }}
      >
        {(dialog?.type === 'create' || dialog?.type === 'edit') && (
          <ProductoraForm
            key={dialog.productora?.id ?? 'new'}
            productora={dialog.productora}
            authToken={authToken}
            onUnauthorized={onUnauthorized}
            onSaved={handleSaved}
            onCancel={closeDialog}
          />
        )}
        {dialog?.type === 'delete' && (
          <>
            <h2 id="prod-delete-title">¿Eliminar "{dialog.productora.nombre}"?</h2>
            <p>Esta acción no se puede deshacer.</p>
            <p className="form-notice">Solo se pueden eliminar productoras sin eventos ni usuarios.</p>
            {deleteError && <p className="login-error" role="alert">{deleteError}</p>}
            <div className="confirm-actions">
              <button type="button" className="logout-btn" onClick={closeDialog} disabled={deleting} autoFocus>
                {deleteError ? 'Cerrar' : 'Cancelar'}
              </button>
              {!deleteError && (
                <button type="button" className="danger-btn" onClick={confirmDelete} disabled={deleting}>
                  {deleting ? 'Eliminando...' : 'Eliminar productora'}
                </button>
              )}
            </div>
          </>
        )}
      </dialog>
    </section>
  )
}

export default ProductorasAdmin
