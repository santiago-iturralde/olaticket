import { useEffect, useRef, useState } from 'react'
import { Link, useLocation, useSearchParams } from 'react-router'
import { FaPlus, FaPen, FaTrashAlt, FaQrcode } from 'react-icons/fa'
import UsersAdmin from './UsersAdmin'
import ProductorasAdmin from './ProductorasAdmin'

const API_URL = import.meta.env.VITE_API_URL

const priceFormatter = new Intl.NumberFormat('es-AR', { style: 'currency', currency: 'ARS' })
const numberFormatter = new Intl.NumberFormat('es-AR')

// "$ 8.000" con un solo precio, "$ 8.000 – $ 14.000" si los sectores tienen distintos.
const priceRange = (sectores) => {
  if (!sectores?.length) return '—'
  const prices = sectores.map((sector) => sector.precio)
  const min = Math.min(...prices)
  const max = Math.max(...prices)
  return min === max ? priceFormatter.format(min) : `${priceFormatter.format(min)} – ${priceFormatter.format(max)}`
}

// Entradas que descontaron stock pero todavía no se pagaron.
const reservedTickets = (evt) => Math.max(0, evt.total_tickets - evt.StockDisponible - evt.entradas_vendidas)

// Pestañas del panel. Productoras es solo del admin; Usuarios, de admins y organizadores
// (el organizador ve solo a su equipo).
const TABS = [
  { id: 'eventos', label: 'Eventos' },
  { id: 'usuarios', label: 'Usuarios' },
  { id: 'productoras', label: 'Productoras', adminOnly: true }
]

function AdminDashboard({ authToken, rol, userID, onUnauthorized }) {
  const location = useLocation()
  const [notice, setNotice] = useState(location.state?.notice || '')
  const [events, setEvents] = useState(null)
  const [error, setError] = useState('')
  const isAdmin = rol === 'admin'
  const tabs = TABS.filter((t) => isAdmin || !t.adminOnly)
  // La pestaña va en la URL (?tab=usuarios): sobrevive a recargar y al botón "atrás".
  const [searchParams, setSearchParams] = useSearchParams()
  const tab = tabs.some((t) => t.id === searchParams.get('tab')) ? searchParams.get('tab') : 'eventos'
  const selectTab = (id) => setSearchParams(id === 'eventos' ? {} : { tab: id })

  const subtitle = {
    eventos: isAdmin ? 'Todos los eventos de la plataforma' : 'Los eventos de tu productora',
    usuarios: isAdmin ? 'Administradores, organizadores y porteros' : 'El equipo de tu productora',
    productoras: 'Las productoras que venden en la plataforma'
  }[tab]

  // Eliminación: evento a confirmar, request en curso y error devuelto por el backend.
  const [pendingDelete, setPendingDelete] = useState(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState('')
  const dialogRef = useRef(null)

  useEffect(() => {
    const controller = new AbortController()
    fetch(`${API_URL}/api/admin/eventos`, {
      headers: { Authorization: `Bearer ${authToken}` },
      signal: controller.signal
    })
      .then(async (res) => {
        if (res.status === 401) {
          onUnauthorized()
          return
        }
        const data = await res.json()
        if (!res.ok) throw new Error(data.error || 'No se pudieron cargar los eventos')
        setEvents(data)
      })
      .catch((err) => {
        if (!controller.signal.aborted) setError(err.message)
      })
    return () => controller.abort()
  }, [authToken, onUnauthorized])

  useEffect(() => {
    const dialog = dialogRef.current
    if (pendingDelete && dialog && !dialog.open) dialog.showModal()
  }, [pendingDelete])

  const askDelete = (evt) => {
    setDeleteError('')
    setPendingDelete(evt)
  }

  const closeDialog = () => {
    if (deleting) return
    dialogRef.current?.close()
  }

  const confirmDelete = async () => {
    setDeleting(true)
    setDeleteError('')
    try {
      const res = await fetch(`${API_URL}/api/admin/eventos/${pendingDelete.id}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${authToken}` }
      })
      if (res.status === 401) {
        onUnauthorized()
        return
      }
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || 'No se pudo eliminar el evento')
      }
      setEvents((prev) => prev.filter((evt) => evt.id !== pendingDelete.id))
      setNotice(`Se eliminó "${pendingDelete.nombre}"`)
      dialogRef.current?.close()
    } catch (err) {
      // fetch rechaza con TypeError cuando no hay conexión (el texto varía según el navegador)
      setDeleteError(err instanceof TypeError ? 'No se pudo conectar con el servidor' : err.message)
    } finally {
      setDeleting(false)
    }
  }

  const totals = (events || []).reduce(
    (acc, evt) => {
      acc.sold += evt.entradas_vendidas
      acc.reserved += reservedTickets(evt)
      acc.revenue += evt.recaudacion
      return acc
    },
    { sold: 0, reserved: 0, revenue: 0 }
  )

  const pendingReserved = pendingDelete ? reservedTickets(pendingDelete) : 0

  return (
    <div className="admin-page">
      <header className="admin-header">
        <div>
          <h1>Mi Panel</h1>
          <p className="admin-subtitle">{subtitle}</p>
        </div>
        {tab === 'eventos' && (
          <Link to="/admin/eventos/nuevo" className="buy-btn admin-new-btn">
            <FaPlus /> Nuevo evento
          </Link>
        )}
      </header>

      <nav className="admin-tabs" aria-label="Secciones del panel">
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            className={`admin-tab${tab === t.id ? ' active' : ''}`}
            aria-current={tab === t.id ? 'page' : undefined}
            onClick={() => selectTab(t.id)}
          >
            {t.label}
          </button>
        ))}
      </nav>

      {notice && <p className="form-success" role="status">{notice}</p>}
      {error && <p className="login-error" role="alert">{error}</p>}

      {tab === 'eventos' && (
        <>
          {events === null && !error && <p className="admin-empty">Cargando eventos...</p>}

          {events?.length === 0 && (
            <div className="admin-empty">
              <p>Todavía no hay eventos cargados.</p>
              <Link to="/admin/eventos/nuevo" className="link-btn">Creá el primero</Link>
            </div>
          )}

          {events?.length > 0 && (
            <>
              <div className="admin-stats">
                <div className="admin-stat">
                  <span>Eventos</span>
                  <b>{numberFormatter.format(events.length)}</b>
                </div>
                <div className="admin-stat">
                  <span>Entradas vendidas</span>
                  <b>{numberFormatter.format(totals.sold)}</b>
                </div>
                <div className="admin-stat">
                  <span>En reserva ahora</span>
                  <b>{numberFormatter.format(totals.reserved)}</b>
                </div>
                <div className="admin-stat highlight-stat">
                  <span>Recaudación real</span>
                  <b>{priceFormatter.format(totals.revenue)}</b>
                </div>
              </div>

              <div className="admin-table-wrapper">
                <table className="admin-table">
                  <thead>
                    <tr>
                      <th scope="col">Evento</th>
                      {isAdmin && <th scope="col">Productora</th>}
                      <th scope="col" className="num">Precios</th>
                      <th scope="col">Vendidas</th>
                      <th scope="col" className="num">Recaudado</th>
                      <th scope="col" className="num">Disponibles</th>
                      <th scope="col"><span className="sr-only">Acciones</span></th>
                    </tr>
                  </thead>
                  <tbody>
                    {events.map((evt) => {
                      const reserved = reservedTickets(evt)
                      const pct = (n) => (evt.total_tickets > 0 ? (n / evt.total_tickets) * 100 : 0)
                      return (
                        <tr key={evt.id}>
                          <td className="admin-event-name">
                            {evt.nombre}
                            <small className="admin-event-meta">
                              {evt.sectores.length === 1 ? '1 sector' : `${evt.sectores.length} sectores`} · máx. {evt.limite_tickets_por_orden} por orden
                            </small>
                          </td>
                          {isAdmin && <td data-label="Productora">{evt.organizador?.nombre}</td>}
                          <td className="num" data-label="Precios">{priceRange(evt.sectores)}</td>
                          <td data-label="Vendidas">
                            <div className="occupancy">
                              <div className="occupancy-bar" aria-hidden="true">
                                <span className="sold" style={{ width: `${pct(evt.entradas_vendidas)}%` }} />
                                <span className="reserved" style={{ width: `${pct(reserved)}%` }} />
                              </div>
                              <span className="occupancy-text">
                                {numberFormatter.format(evt.entradas_vendidas)} / {numberFormatter.format(evt.total_tickets)}
                                {reserved > 0 && <small> +{numberFormatter.format(reserved)} en reserva</small>}
                              </span>
                            </div>
                          </td>
                          <td className="num" data-label="Recaudado">{priceFormatter.format(evt.recaudacion)}</td>
                          <td className="num" data-label="Disponibles">{evt.StockDisponible > 0 ? numberFormatter.format(evt.StockDisponible) : 'Agotado'}</td>
                          <td className="actions">
                            <div className="row-actions">
                              <Link to={`/admin/eventos/${evt.id}/escanear`} className="logout-btn admin-edit-btn">
                                <FaQrcode aria-hidden="true" /> Escanear
                              </Link>
                              <Link to={`/admin/eventos/${evt.id}/editar`} className="logout-btn admin-edit-btn">
                                <FaPen aria-hidden="true" /> Editar
                              </Link>
                              <button
                                type="button"
                                className="admin-delete-btn"
                                onClick={() => askDelete(evt)}
                                aria-label={`Eliminar ${evt.nombre}`}
                                title="Eliminar evento"
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
        </>
      )}

      {tab === 'usuarios' && <UsersAdmin authToken={authToken} rol={rol} userID={userID} onUnauthorized={onUnauthorized} />}
      {tab === 'productoras' && <ProductorasAdmin authToken={authToken} onUnauthorized={onUnauthorized} />}

      <dialog
        ref={dialogRef}
        className="confirm-dialog"
        aria-labelledby="delete-dialog-title"
        onClose={() => setPendingDelete(null)}
        onCancel={(event) => {
          if (deleting) event.preventDefault()
        }}
      >
        {pendingDelete && (
          <>
            <h2 id="delete-dialog-title">¿Eliminar "{pendingDelete.nombre}"?</h2>
            <p>Esta acción no se puede deshacer.</p>
            {pendingReserved > 0 && (
              <p className="confirm-warning">
                Hay {numberFormatter.format(pendingReserved)} entradas en reserva: esas compras se van a cancelar sin cobro.
              </p>
            )}
            {deleteError && <p className="login-error" role="alert">{deleteError}</p>}
            <div className="confirm-actions">
              <button type="button" className="logout-btn" onClick={closeDialog} disabled={deleting} autoFocus>
                {deleteError ? 'Cerrar' : 'Cancelar'}
              </button>
              {!deleteError && (
                <button type="button" className="danger-btn" onClick={confirmDelete} disabled={deleting}>
                  {deleting ? 'Eliminando...' : 'Eliminar evento'}
                </button>
              )}
            </div>
          </>
        )}
      </dialog>
    </div>
  )
}

export default AdminDashboard
