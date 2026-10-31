import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { FaArrowLeft, FaPlus, FaTrashAlt, FaImage } from 'react-icons/fa'

const API_URL = import.meta.env.VITE_API_URL

// Mismos límites que valida el backend (eventos.nombre VARCHAR(100), sectores.nombre VARCHAR(60),
// sectores.precio DECIMAL(10,2)).
const MAX_NAME_LENGTH = 100
const MAX_SECTOR_NAME_LENGTH = 60
const MAX_PRICE = 99999999.99
const MAX_SECTORES = 20
const MAX_CAPACITY = 1000000
const MAX_LIMIT = 20
const DEFAULT_LIMIT = 4
const MAX_IMAGE_MB = 5
const IMAGE_TYPES = ['image/jpeg', 'image/png', 'image/webp']

const priceFormatter = new Intl.NumberFormat('es-AR', { style: 'currency', currency: 'ARS' })
const numberFormatter = new Intl.NumberFormat('es-AR')

const EMPTY_FORM = { nombre: '', limite_tickets_por_orden: String(DEFAULT_LIMIT), organizador_id: '' }

// key es solo para React; id es el del backend (undefined = sector nuevo).
// sold = entradas vendidas o reservadas: la capacidad no puede bajar de eso ni el sector quitarse.
let nextKey = 0
const newSector = (data = {}) => ({
  key: ++nextKey,
  id: data.id,
  nombre: data.nombre ?? '',
  precio: data.precio !== undefined ? String(data.precio) : '',
  capacidad: data.capacidad !== undefined ? String(data.capacidad) : '',
  sold: data.capacidad !== undefined ? data.capacidad - data.stock_disponible : 0
})

// Los errores de sector usan la misma clave que el backend: "sectores.<índice>.<campo>".
const sectorKey = (index, field) => `sectores.${index}.${field}`

const validate = (form, sectores, { needsOrganizador }) => {
  const errors = {}
  const nombre = form.nombre.trim()
  const limite = Number(form.limite_tickets_por_orden)

  if (!nombre) errors.nombre = 'Ingresá el nombre del evento'
  else if (nombre.length > MAX_NAME_LENGTH) errors.nombre = `Máximo ${MAX_NAME_LENGTH} caracteres`
  if (!Number.isInteger(limite) || limite < 1 || limite > MAX_LIMIT) {
    errors.limite_tickets_por_orden = `Ingresá un número entero entre 1 y ${MAX_LIMIT}`
  }
  if (needsOrganizador && !form.organizador_id) errors.organizador_id = 'Elegí la productora'

  if (sectores.length === 0) errors.sectores = 'Agregá al menos un sector'
  const seen = new Set()
  sectores.forEach((sector, i) => {
    const sectorNombre = sector.nombre.trim()
    const precio = Number(sector.precio)
    const capacidad = Number(sector.capacidad)

    if (!sectorNombre) errors[sectorKey(i, 'nombre')] = 'Ingresá el nombre'
    else if (sectorNombre.length > MAX_SECTOR_NAME_LENGTH) errors[sectorKey(i, 'nombre')] = `Máximo ${MAX_SECTOR_NAME_LENGTH} caracteres`
    else if (seen.has(sectorNombre.toLowerCase())) errors[sectorKey(i, 'nombre')] = 'Ya hay otro sector con ese nombre'
    seen.add(sectorNombre.toLowerCase())

    if (sector.precio === '' || !Number.isFinite(precio) || precio <= 0) errors[sectorKey(i, 'precio')] = 'Debe ser mayor a cero'
    else if (precio > MAX_PRICE) errors[sectorKey(i, 'precio')] = 'Es demasiado alto'

    if (!Number.isInteger(capacidad) || capacidad <= 0) errors[sectorKey(i, 'capacidad')] = 'Entero mayor a cero'
    else if (capacidad > MAX_CAPACITY) errors[sectorKey(i, 'capacidad')] = 'Es demasiado alta'
    else if (capacidad < sector.sold) errors[sectorKey(i, 'capacidad')] = `Mínimo ${sector.sold} (ya vendidas o reservadas)`
  })
  return errors
}

// Imágenes: url = la guardada en el backend, file = la elegida ahora (reemplaza a url) y
// preview su data URL, remove = quitar la guardada al guardar.
const EMPTY_IMAGES = {
  portada: { url: '', file: null, preview: '', remove: false },
  mapa: { url: '', file: null, preview: '', remove: false }
}

const validateImage = (file) => {
  if (!IMAGE_TYPES.includes(file.type)) return 'Tiene que ser JPG, PNG o WebP'
  if (file.size > MAX_IMAGE_MB * 1024 * 1024) return `Máximo ${MAX_IMAGE_MB} MB`
  return null
}

// Muestra la imagen elegida o, si no hay, la guardada.
function ImageField({ name, label, hint, image, error, onSelect, onRemove }) {
  const inputRef = useRef(null)
  const src = image.preview || (image.remove ? '' : image.url)
  const inputID = `event-${name}`

  return (
    <div className="form-field image-field">
      <label htmlFor={inputID}>{label}</label>
      <div className={`image-preview${src ? '' : ' empty'}`}>
        {src ? <img src={src} alt={`Vista previa: ${label.toLowerCase()}`} /> : <FaImage aria-hidden="true" />}
      </div>
      <div className="image-actions">
        <input
          ref={inputRef}
          id={inputID}
          type="file"
          accept={IMAGE_TYPES.join(',')}
          onChange={(event) => {
            const file = event.target.files?.[0]
            if (file) onSelect(file)
            event.target.value = '' // Permite volver a elegir el mismo archivo
          }}
          aria-invalid={error ? 'true' : undefined}
          aria-describedby={error ? `${inputID}-error` : `${inputID}-hint`}
          className="sr-only"
          tabIndex={-1}
        />
        {/* El input queda oculto: el botón es el control accesible por teclado. */}
        <button
          type="button"
          className="logout-btn"
          onClick={() => inputRef.current?.click()}
          aria-label={`${label}: ${src ? 'cambiar' : 'elegir'} imagen`}
          aria-describedby={error ? `${inputID}-error` : `${inputID}-hint`}
        >
          {src ? 'Cambiar imagen' : 'Elegir imagen'}
        </button>
        {src && (
          <button type="button" className="link-btn" onClick={onRemove} aria-label={`Quitar ${label.toLowerCase()}`}>
            Quitar
          </button>
        )}
      </div>
      {error ? (
        <span id={`${inputID}-error`} className="field-error">{error}</span>
      ) : (
        <span id={`${inputID}-hint`} className="form-notice">{hint}</span>
      )}
    </div>
  )
}

// Al quitar o agregar filas cambian los índices: descartamos los errores de sectores.
const withoutSectorErrors = (errors) =>
  Object.fromEntries(Object.entries(errors).filter(([key]) => !key.startsWith('sectores')))

function EventForm({ authToken, rol, onUnauthorized }) {
  const { id } = useParams()
  const isEdit = Boolean(id)
  const isAdmin = rol === 'admin'
  const needsOrganizador = isAdmin && !isEdit
  const navigate = useNavigate()

  const [form, setForm] = useState(EMPTY_FORM)
  const [sectores, setSectores] = useState(() => (isEdit ? [] : [newSector({ nombre: 'General' })]))
  const [images, setImages] = useState(EMPTY_IMAGES)
  const [organizadores, setOrganizadores] = useState([])
  const [errors, setErrors] = useState({})
  const [formError, setFormError] = useState('')
  const [loadError, setLoadError] = useState('')
  const [loading, setLoading] = useState(isEdit)
  const [saving, setSaving] = useState(false)
  const lastSectorRef = useRef(null)
  const focusNewSector = useRef(false)

  // Edición: precargamos el evento (el backend devuelve 404 si es de otra productora).
  useEffect(() => {
    if (!isEdit) return
    const controller = new AbortController()
    fetch(`${API_URL}/api/admin/eventos/${id}`, {
      headers: { Authorization: `Bearer ${authToken}` },
      signal: controller.signal
    })
      .then(async (res) => {
        if (res.status === 401) {
          onUnauthorized()
          return
        }
        const data = await res.json()
        if (!res.ok) throw new Error(data.error || 'No se pudo cargar el evento')
        setForm({
          nombre: data.nombre,
          limite_tickets_por_orden: String(data.limite_tickets_por_orden),
          organizador_id: String(data.organizador_id)
        })
        setSectores(data.sectores.map(newSector))
        setImages({
          portada: { ...EMPTY_IMAGES.portada, url: data.portada_url || '' },
          mapa: { ...EMPTY_IMAGES.mapa, url: data.mapa_url || '' }
        })
      })
      .catch((err) => {
        if (!controller.signal.aborted) setLoadError(err.message)
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [isEdit, id, authToken, onUnauthorized])

  // Alta como admin: hay que elegir a qué productora pertenece el evento.
  useEffect(() => {
    if (!needsOrganizador) return
    const controller = new AbortController()
    fetch(`${API_URL}/api/admin/organizadores`, {
      headers: { Authorization: `Bearer ${authToken}` },
      signal: controller.signal
    })
      .then(async (res) => {
        const data = await res.json()
        if (!res.ok) throw new Error(data.error || 'No se pudieron cargar las productoras')
        setOrganizadores(data)
      })
      .catch((err) => {
        if (!controller.signal.aborted) setFormError(err.message)
      })
    return () => controller.abort()
  }, [needsOrganizador, authToken])

  // Al agregar un sector llevamos el foco a su nombre.
  useEffect(() => {
    if (focusNewSector.current) {
      focusNewSector.current = false
      lastSectorRef.current?.focus()
    }
  }, [sectores.length])

  const handleChange = (event) => {
    const { name, value } = event.target
    setForm((prev) => ({ ...prev, [name]: value }))
    setErrors((prev) => ({ ...prev, [name]: undefined }))
  }

  const handleSectorChange = (index, field) => (event) => {
    const { value } = event.target
    setSectores((prev) => prev.map((sector, i) => (i === index ? { ...sector, [field]: value } : sector)))
    setErrors((prev) => ({ ...prev, [sectorKey(index, field)]: undefined, sectores: undefined }))
  }

  const addSector = () => {
    focusNewSector.current = true
    setSectores((prev) => [...prev, newSector()])
    setErrors((prev) => withoutSectorErrors(prev))
  }

  const selectImage = (name) => (file) => {
    const error = validateImage(file)
    setErrors((prev) => ({ ...prev, [name]: error || undefined }))
    if (error) return
    const reader = new FileReader()
    reader.onload = () =>
      setImages((prev) => ({ ...prev, [name]: { ...prev[name], file, preview: reader.result, remove: false } }))
    reader.onerror = () => setErrors((prev) => ({ ...prev, [name]: 'No se pudo leer la imagen' }))
    reader.readAsDataURL(file)
  }

  // Si había una elegida se descarta; si no, se marca la guardada para quitar.
  const removeImage = (name) => () => {
    setErrors((prev) => ({ ...prev, [name]: undefined }))
    setImages((prev) => ({ ...prev, [name]: { ...prev[name], file: null, preview: '', remove: !prev[name].file } }))
  }

  const removeSector = (index) => {
    setSectores((prev) => prev.filter((_, i) => i !== index))
    setErrors((prev) => withoutSectorErrors(prev))
  }

  const handleSubmit = async (event) => {
    event.preventDefault()
    setFormError('')
    const nextErrors = validate(form, sectores, { needsOrganizador })
    setErrors(nextErrors)
    if (Object.keys(nextErrors).length > 0) return

    const body = {
      nombre: form.nombre.trim(),
      limite_tickets_por_orden: Number(form.limite_tickets_por_orden),
      sectores: sectores.map((sector) => ({
        ...(sector.id ? { id: sector.id } : {}),
        nombre: sector.nombre.trim(),
        precio: Number(sector.precio),
        capacidad: Number(sector.capacidad)
      }))
    }
    if (needsOrganizador) body.organizador_id = Number(form.organizador_id)

    // multipart: los datos van como JSON en "datos" y las imágenes como archivos.
    // Sin Content-Type a mano: el navegador agrega el boundary.
    const formData = new FormData()
    formData.append('datos', JSON.stringify(body))
    for (const name of ['portada', 'mapa']) {
      const image = images[name]
      if (image.file) formData.append(name, image.file)
      else if (image.remove && image.url) formData.append(`quitar_${name}`, 'true')
    }

    setSaving(true)
    try {
      const res = await fetch(`${API_URL}/api/admin/eventos${isEdit ? `/${id}` : ''}`, {
        method: isEdit ? 'PUT' : 'POST',
        headers: { Authorization: `Bearer ${authToken}` },
        body: formData
      })
      if (res.status === 401) {
        onUnauthorized()
        return
      }
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        if (data.field) setErrors({ [data.field]: data.error })
        else setFormError(data.error || 'No se pudo guardar el evento')
        return
      }
      navigate('/admin', {
        state: { notice: isEdit ? `Se actualizó "${data.nombre}"` : `Se creó "${data.nombre}"` }
      })
    } catch {
      setFormError('No se pudo conectar con el servidor')
    } finally {
      setSaving(false)
    }
  }

  const fieldProps = (name) => ({
    id: `event-${name}`,
    name,
    value: form[name],
    onChange: handleChange,
    'aria-invalid': errors[name] ? 'true' : undefined,
    'aria-describedby': errors[name] ? `event-${name}-error` : undefined
  })

  const sectorFieldProps = (index, field) => {
    const key = sectorKey(index, field)
    return {
      id: `event-${key}`,
      value: sectores[index][field],
      onChange: handleSectorChange(index, field),
      'aria-invalid': errors[key] ? 'true' : undefined,
      'aria-describedby': errors[key] ? `event-${key}-error` : undefined
    }
  }

  const fieldError = (name) =>
    errors[name] && <span id={`event-${name}-error`} className="field-error">{errors[name]}</span>

  const totalCapacity = sectores.reduce((sum, sector) => sum + (Number(sector.capacidad) || 0), 0)
  const prices = sectores.map((sector) => Number(sector.precio)).filter((precio) => precio > 0)
  const minPrice = prices.length > 0 ? Math.min(...prices) : 0

  return (
    <div className="admin-page">
      <Link to="/admin" className="link-btn admin-back"><FaArrowLeft aria-hidden="true" /> Volver al panel</Link>

      <form className="login-card admin-form" onSubmit={handleSubmit} noValidate>
        <h1>{isEdit ? 'Editar evento' : 'Nuevo evento'}</h1>

        {loading ? (
          <p className="form-notice">Cargando evento...</p>
        ) : loadError ? (
          <p className="login-error" role="alert">{loadError}</p>
        ) : (
          <>
            <div className="form-field">
              <label htmlFor="event-nombre">Nombre</label>
              <input {...fieldProps('nombre')} maxLength={MAX_NAME_LENGTH} autoComplete="off" />
              {fieldError('nombre')}
            </div>

            <div className="form-field">
              <label htmlFor="event-limite_tickets_por_orden">Límite de entradas por orden</label>
              <input
                {...fieldProps('limite_tickets_por_orden')}
                type="number"
                inputMode="numeric"
                min="1"
                max={MAX_LIMIT}
                step="1"
                className="limit-input"
              />
              {fieldError('limite_tickets_por_orden') || (
                <span className="form-notice">Cuántas entradas puede llevar una persona en cada compra (máximo {MAX_LIMIT}).</span>
              )}
            </div>

            <div className="image-grid">
              <ImageField
                name="portada"
                label="Portada"
                hint={`Se muestra en la tienda. JPG, PNG o WebP, hasta ${MAX_IMAGE_MB} MB.`}
                image={images.portada}
                error={errors.portada}
                onSelect={selectImage('portada')}
                onRemove={removeImage('portada')}
              />
              <ImageField
                name="mapa"
                label="Mapa de sectores"
                hint="Opcional. El comprador lo ve al elegir sus entradas."
                image={images.mapa}
                error={errors.mapa}
                onSelect={selectImage('mapa')}
                onRemove={removeImage('mapa')}
              />
            </div>

            <fieldset className="sector-fieldset" aria-describedby={errors.sectores ? 'event-sectores-error' : undefined}>
              <legend>Sectores</legend>
              <p className="form-notice">Cada sector tiene su precio y su capacidad. El comprador elige uno al comprar.</p>

              <ul className="sector-list">
                {sectores.map((sector, i) => {
                  const locked = sector.sold > 0
                  return (
                    <li key={sector.key} className="sector-row">
                      <div className="form-field sector-name">
                        <label htmlFor={`event-${sectorKey(i, 'nombre')}`}>Nombre del sector</label>
                        <input
                          {...sectorFieldProps(i, 'nombre')}
                          ref={i === sectores.length - 1 ? lastSectorRef : undefined}
                          maxLength={MAX_SECTOR_NAME_LENGTH}
                          placeholder="Ej: Platea VIP"
                          autoComplete="off"
                        />
                        {fieldError(sectorKey(i, 'nombre'))}
                      </div>
                      <div className="form-field">
                        <label htmlFor={`event-${sectorKey(i, 'precio')}`}>Precio (ARS)</label>
                        <input {...sectorFieldProps(i, 'precio')} type="number" inputMode="decimal" min="0.01" step="0.01" />
                        {fieldError(sectorKey(i, 'precio'))}
                      </div>
                      <div className="form-field">
                        <label htmlFor={`event-${sectorKey(i, 'capacidad')}`}>Capacidad</label>
                        <input
                          {...sectorFieldProps(i, 'capacidad')}
                          type="number"
                          inputMode="numeric"
                          min={Math.max(1, sector.sold)}
                          step="1"
                        />
                        {fieldError(sectorKey(i, 'capacidad')) ||
                          (locked && <span className="form-notice">{numberFormatter.format(sector.sold)} vendidas o reservadas</span>)}
                      </div>
                      <button
                        type="button"
                        className="admin-delete-btn sector-remove"
                        onClick={() => removeSector(i)}
                        disabled={locked || sectores.length === 1}
                        aria-label={`Quitar sector ${sector.nombre || i + 1}`}
                        title={
                          locked
                            ? 'No se puede quitar: tiene entradas vendidas o reservadas'
                            : sectores.length === 1 ? 'El evento necesita al menos un sector' : 'Quitar sector'
                        }
                      >
                        <FaTrashAlt aria-hidden="true" />
                      </button>
                    </li>
                  )
                })}
              </ul>

              {fieldError('sectores')}

              <div className="sector-footer">
                <button
                  type="button"
                  className="logout-btn sector-add"
                  onClick={addSector}
                  disabled={sectores.length >= MAX_SECTORES}
                >
                  <FaPlus aria-hidden="true" /> Agregar sector
                </button>
                <span className="form-notice">
                  Capacidad total: <b>{numberFormatter.format(totalCapacity)}</b>
                  {sectores.length > 1 && minPrice > 0 && <> · desde {priceFormatter.format(minPrice)}</>}
                </span>
              </div>
            </fieldset>

            {needsOrganizador && (
              <div className="form-field">
                <label htmlFor="event-organizador_id">Productora</label>
                <select {...fieldProps('organizador_id')}>
                  <option value="">Elegí una productora</option>
                  {organizadores.map((org) => (
                    <option key={org.id} value={org.id}>{org.nombre}</option>
                  ))}
                </select>
                {fieldError('organizador_id')}
              </div>
            )}

            {formError && <p className="login-error" role="alert">{formError}</p>}

            <button className="buy-btn" type="submit" disabled={saving}>
              {saving ? 'GUARDANDO...' : isEdit ? 'Guardar cambios' : 'Crear evento'}
            </button>
          </>
        )}
      </form>
    </div>
  )
}

export default EventForm
