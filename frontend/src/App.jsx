import { useState, useEffect, useCallback, lazy, Suspense } from 'react'
import { Routes, Route, Link, Navigate, useLocation, useNavigate } from 'react-router'
import { QRCodeCanvas } from 'qrcode.react' // <--- CORREGIDO AQUÍ
import { FaMapMarkerAlt, FaCalendarAlt, FaClock, FaTicketAlt, FaArrowLeft, FaUsers, FaMinus, FaPlus } from 'react-icons/fa'
import Login from './components/Login'
import Register from './components/Register'
import ForgotPassword from './components/ForgotPassword'
import ResetPassword from './components/ResetPassword'
import ProtectedRoute from './components/ProtectedRoute'
import AdminDashboard from './components/AdminDashboard'
import EventForm from './components/EventForm'
import { PANEL_ROLES, SCANNER_APP_PATH, SCANNER_ROLES, canAccessPanel, decodeTokenClaims } from './components/session'
import './App.css'

import { API_URL } from './components/apiConfig'

// El escáner (html5-qrcode) pesa más que el resto de la tienda: se baja solo al abrirlo.
const EventScanner = lazy(() => import('./components/EventScanner'))
const ScannerApp = lazy(() => import('./components/ScannerApp'))

// src/App.jsx

// src/App.jsx

// src/App.jsx

const EVENT_IMAGES = [
  // 1. RAC vs Estudiantes (Pelota en el pasto)
  "/img/racest.png",

  // 2. Argentina vs Venezuela (ESTADIO LLENO - Fuente: Pexels)
  "/img/argVSven.png",

  // 3. Bonsifest (Fiesta / Concierto)
  "/img/bonsi.png"
]

const FINAL_STATUSES = ['confirmada', 'rechazada', 'expirada']

const priceFormatter = new Intl.NumberFormat('es-AR', { style: 'currency', currency: 'ARS' })
const formatPrice = (value) => priceFormatter.format(value || 0)

const formatCountdown = (ms) => {
  const totalSeconds = Math.ceil(ms / 1000)
  const minutes = String(Math.floor(totalSeconds / 60)).padStart(2, '0')
  const seconds = String(totalSeconds % 60).padStart(2, '0')
  return `${minutes}:${seconds}`
}

// Mercado Pago nos devuelve a /?order_id=X (back_urls) después de pagar
const orderIDFromUrl = () => {
  const id = Number(new URLSearchParams(window.location.search).get('order_id'))
  return Number.isSafeInteger(id) && id > 0 ? id : null
}

// El email de recuperación trae a /?reset_token=X
const resetTokenFromUrl = () => new URLSearchParams(window.location.search).get('reset_token') || ''

// Parámetros que la app lee una sola vez al cargar y después saca de la URL.
const CONSUMED_URL_PARAMS = ['order_id', 'reset_token']

// Orden en curso: sobrevive a recargar la página y a un nuevo login (si venció la sesión).
// Su detalle siempre se pide al backend (GET /orders/:id), que es la fuente de verdad.
const PENDING_ORDER_KEY = 'pending_order_id'

const storedOrderID = () => {
  const id = Number(localStorage.getItem(PENDING_ORDER_KEY))
  return Number.isSafeInteger(id) && id > 0 ? id : null
}

const initialOrderID = () => orderIDFromUrl() ?? storedOrderID()

// Vistas que muestran la orden en curso: solo en ellas se aplica lo que llega del backend.
const ORDER_VIEWS = ['QUEUE', 'CHECKOUT', 'TICKET']

// Una plaza por entrada de la orden, en el orden de sus ítems: cada una pide su asistente.
const attendeeSlots = (order) =>
  (order?.items || []).flatMap((item) =>
    Array.from({ length: item.cantidad }, () => ({ sector_id: item.sector_id, sector: item.sector }))
  )

// Mismas reglas que el backend: nombre con letras (3-100) y documento de 6 a 12
// letras/números, sin repetir dentro de la compra. Puntos, guiones y espacios no cuentan.
const normalizeDNI = (dni) => dni.replace(/[.\-\s]/g, '').toUpperCase()
const attendeeKey = (index, field) => `asistentes.${index}.${field}`

const validateAttendees = (attendees) => {
  const errors = {}
  const seen = new Set()
  attendees.forEach((attendee, i) => {
    const nombre = attendee.nombre.trim().replace(/\s+/g, ' ')
    const dni = normalizeDNI(attendee.dni)
    if (nombre.length < 3 || nombre.length > 100 || !/\p{L}/u.test(nombre)) errors[attendeeKey(i, 'nombre')] = 'Ingresá nombre y apellido'
    if (!/^[A-Z0-9]{6,12}$/.test(dni)) errors[attendeeKey(i, 'dni')] = 'Ingresá un DNI válido (solo números)'
    else if (seen.has(dni)) errors[attendeeKey(i, 'dni')] = 'Ese DNI ya está en otra entrada'
    seen.add(dni)
  })
  return errors
}

const orderTotal = (order) => order.items.reduce((sum, item) => sum + item.precio_unitario * item.cantidad, 0)

// Precio más bajo entre los sectores con stock (o entre todos si está agotado).
const minPrice = (evt) => {
  const sectores = evt.sectores || []
  const available = sectores.filter((sector) => sector.stock_disponible > 0)
  const prices = (available.length > 0 ? available : sectores).map((sector) => sector.precio)
  return prices.length > 0 ? Math.min(...prices) : 0
}

// Carrito: { [sector_id]: cantidad }. Lo ajusta al stock actual (descarta lo agotado)
// y al límite por orden del evento, que cuenta todas las entradas sumando sectores.
const clampCart = (evt, cart) => {
  const next = {}
  let left = evt.limite_tickets_por_orden
  for (const sector of evt.sectores) {
    const cantidad = Math.min(cart[sector.id] || 0, sector.stock_disponible, left)
    if (cantidad > 0) {
      next[sector.id] = cantidad
      left -= cantidad
    }
  }
  return next
}

const cartQuantity = (cart) => Object.values(cart).reduce((sum, n) => sum + n, 0)

const ticketsLabel = (n) => (n === 1 ? '1 entrada' : `${n} entradas`)

const timeFormatter = new Intl.DateTimeFormat('es-AR', { hour: '2-digit', minute: '2-digit' })

function App() {
  const [authToken, setAuthToken] = useState(() => localStorage.getItem('token'))
  const [userID, setUserID] = useState(() => {
    const storedUserID = Number(localStorage.getItem('user_id'))
    return Number.isSafeInteger(storedUserID) && storedUserID > 0 ? storedUserID : null
  })
  const [resetToken] = useState(resetTokenFromUrl)
  const [loginNotice, setLoginNotice] = useState('')
  const [view, setView] = useState(() => {
    if (resetTokenFromUrl()) return 'RESET_PASSWORD'
    if (!localStorage.getItem('token')) return 'LOGIN'
    return initialOrderID() ? 'QUEUE' : 'HOME'
  })
  const [events, setEvents] = useState([])

  // Vista del evento: carrito con las entradas de cada sector antes de comprar
  const [selectedEvent, setSelectedEvent] = useState(null)
  const [cart, setCart] = useState({})
  const [buying, setBuying] = useState(false)
  const [buyError, setBuyError] = useState('')
  // Compra en curso (fila, checkout y entradas): order es el GET /orders/:id
  const [orderID, setOrderID] = useState(initialOrderID)
  const [order, setOrder] = useState(null)
  const [status, setStatus] = useState(null)
  const [error, setError] = useState(null)
  // Asistentes del checkout: uno por entrada ({ nombre, dni })
  const [attendees, setAttendees] = useState([])
  const [attendeeErrors, setAttendeeErrors] = useState({})

  // Checkout / reserva temporal
  const [expiresAt, setExpiresAt] = useState(null)
  const [now, setNow] = useState(() => Date.now())
  const [checkoutLoading, setCheckoutLoading] = useState(false)
  const [checkoutError, setCheckoutError] = useState('')

  // El rol sale del JWT: el backend lo vuelve a verificar en cada request de /api/admin.
  const rol = authToken ? decodeTokenClaims(authToken)?.rol : undefined
  const navigate = useNavigate()
  const location = useLocation()
  const inPanel = location.pathname.startsWith('/admin')

  // 1. CARGAR EVENTOS (también al volver del panel, por si se editó alguno)
  useEffect(() => {
    if (inPanel) return
    fetch(`${API_URL}/events`)
      .then(res => res.json())
      .then(data => setEvents(Array.isArray(data) ? data : [])) // null o error del API: lista vacía, no pantalla en blanco
      .catch(err => console.error("Error cargando eventos:", err))
  }, [inPanel])

  // Limpiamos ?order_id= y ?reset_token= de la URL una vez leídos (se toman al iniciar).
  // Solo esos: el resto de la query es de la pantalla (ej. ?tab=usuarios del panel).
  // Vía el router, para no pisar su estado de navegación.
  useEffect(() => {
    const params = new URLSearchParams(location.search)
    if (!CONSUMED_URL_PARAMS.some((name) => params.has(name))) return
    CONSUMED_URL_PARAMS.forEach((name) => params.delete(name))
    const rest = params.toString()
    navigate(location.pathname + (rest ? `?${rest}` : ''), { replace: true })
  }, [location.search, location.pathname, navigate])

  // Login y registro devuelven el mismo { token, user_id, rol }
  const handleAuthenticated = (data) => {
    localStorage.setItem('token', data.token)
    localStorage.setItem('user_id', String(data.user_id))
    setAuthToken(data.token)
    setUserID(data.user_id)
    setLoginNotice('')
    setView(orderID ? 'QUEUE' : 'HOME')
    // El portero va directo a la app de puerta (no usa la tienda ni el panel).
    if (data.rol === 'portero') navigate(SCANNER_APP_PATH, { replace: true })
  }

  // Tras cambiar la clave pedimos un login nuevo (también si había una sesión abierta).
  const handlePasswordReset = (message) => {
    handleLogout()
    setLoginNotice(message)
  }

  const goTo = (nextView) => () => {
    setLoginNotice('')
    setView(nextView)
  }

  const handleLogout = useCallback(() => {
    localStorage.removeItem('token')
    localStorage.removeItem('user_id')
    localStorage.removeItem(PENDING_ORDER_KEY)
    localStorage.removeItem('checkout_event') // Formato anterior
    setAuthToken(null)
    setUserID(null)
    setOrderID(null)
    setOrder(null)
    setSelectedEvent(null)
    setStatus(null)
    setError(null)
    setExpiresAt(null)
    setCheckoutError('')
    setAttendees([])
    setAttendeeErrors({})
    setView('LOGIN')
    navigate('/')
  }, [navigate])

  // Token vencido (401): pedimos login de nuevo pero conservamos la orden en curso,
  // así al volver a entrar se retoma donde estaba (handleAuthenticated -> QUEUE).
  const handleSessionExpired = useCallback(() => {
    localStorage.removeItem('token')
    localStorage.removeItem('user_id')
    setAuthToken(null)
    setUserID(null)
    setLoginNotice('Tu sesión expiró. Iniciá sesión de nuevo para continuar.')
    setView('LOGIN')
    navigate('/')
  }, [navigate])

  // Trae el detalle de la orden y muestra la pantalla de su estado (solo si el usuario sigue
  // en una vista de la orden: si ya se fue a la home o al panel no lo movemos). Es lo que permite
  // volver de Mercado Pago (o recargar) y ver las entradas: no depende del SSE ni de
  // lo que haya quedado en el navegador.
  const loadOrder = useCallback(async (id) => {
    try {
      const res = await fetch(`${API_URL}/orders/${id}`, { headers: { Authorization: `Bearer ${authToken}` } })
      if (res.status === 401) {
        handleSessionExpired()
        return
      }
      const data = await res.json().catch(() => ({}))
      if (res.status === 404) {
        localStorage.removeItem(PENDING_ORDER_KEY)
        setStatus('rechazada')
        setError('No encontramos esta orden en tu cuenta. ¿Iniciaste sesión con otro usuario?')
        setView((current) => (ORDER_VIEWS.includes(current) ? 'QUEUE' : current))
        return
      }
      if (!res.ok) throw new Error(data.error || `Error ${res.status}`)

      setOrder(data)
      setStatus(data.status)
      switch (data.status) {
        case 'reservada': {
          setNow(Date.now())
          setExpiresAt(Date.now() + (data.expires_in || 0) * 1000)
          const slots = attendeeSlots(data)
          // La primera entrada se propone a nombre del comprador; si ya cargó datos, se respetan.
          setAttendees((prev) =>
            prev.length === slots.length
              ? prev
              : slots.map((_, i) => (i === 0 ? { nombre: data.comprador?.nombre || '', dni: data.comprador?.dni || '' } : { nombre: '', dni: '' }))
          )
          setView((current) => (ORDER_VIEWS.includes(current) ? 'CHECKOUT' : current))
          break
        }
        case 'confirmada':
          localStorage.removeItem(PENDING_ORDER_KEY)
          setView((current) => (ORDER_VIEWS.includes(current) ? 'TICKET' : current))
          break
        case 'rechazada':
        case 'expirada':
          localStorage.removeItem(PENDING_ORDER_KEY)
          setError(data.mensaje)
          setView((current) => (ORDER_VIEWS.includes(current) ? 'QUEUE' : current))
          break
        default:
          setView((current) => (ORDER_VIEWS.includes(current) ? 'QUEUE' : current))
      }
    } catch (err) {
      console.error('Error cargando la orden:', err)
    }
  }, [authToken, handleSessionExpired])

  // Al abrir la app con una orden en curso (vuelta de Mercado Pago, recarga, nuevo login).
  useEffect(() => {
    if (authToken && orderID) loadOrder(orderID)
  }, [authToken, orderID, loadOrder])

  const goHome = () => {
    setView(authToken ? 'HOME' : 'LOGIN')
    navigate('/')
  }

  // 2. Escuchar el estado de la orden por SSE autenticado.
  // El mismo stream sigue abierto en la fila y en el checkout (mientras el usuario
  // paga en Mercado Pago) hasta recibir un estado final.
  const isTracking = view === 'QUEUE' || view === 'CHECKOUT'

  useEffect(() => {
    if (!isTracking || !orderID || !authToken) {
      return
    }

    const controller = new AbortController()

    // Devuelve true cuando el estado es final y hay que cerrar el stream.
    // Reservada y confirmada recargan la orden: traen los ítems y las entradas.
    const handleUpdate = (update) => {
      setStatus(update.status)
      switch (update.status) {
        case 'reservada':
          loadOrder(orderID)
          return false
        case 'confirmada':
          loadOrder(orderID)
          return true
        case 'rechazada':
        case 'expirada':
          localStorage.removeItem(PENDING_ORDER_KEY)
          setError(update.mensaje || (update.status === 'expirada'
            ? 'Tu reserva expiró y las entradas fueron liberadas.'
            : 'Lo sentimos, se agotó el stock o el pago fue rechazado.'))
          setView('QUEUE')
          return true
        default:
          return false
      }
    }

    const readStream = async () => {
      // Reintentamos si la conexión se corta antes de un estado final
      while (!controller.signal.aborted) {
        try {
          const res = await fetch(`${API_URL}/orders/${orderID}/stream`, {
            headers: { Authorization: `Bearer ${authToken}` },
            signal: controller.signal
          })
          // 401 = venció la sesión (no es que la orden no exista): login y se retoma.
          if (res.status === 401) {
            handleSessionExpired()
            return
          }
          if (res.status >= 400 && res.status < 500) {
            localStorage.removeItem(PENDING_ORDER_KEY)
            setStatus('rechazada')
            setError('No encontramos esta orden en tu cuenta. ¿Iniciaste sesión con otro usuario?')
            setView('QUEUE')
            return
          }
          if (!res.ok || !res.body) {
            throw new Error(`Error abriendo el stream (${res.status})`)
          }

          const reader = res.body.getReader()
          const decoder = new TextDecoder()
          let buffer = ''

          while (!controller.signal.aborted) {
            const { done, value } = await reader.read()
            if (done) break

            buffer += decoder.decode(value, { stream: true })
            buffer = buffer.replace(/\r\n/g, '\n')
            let boundary = buffer.indexOf('\n\n')
            while (boundary !== -1) {
              const frame = buffer.slice(0, boundary)
              buffer = buffer.slice(boundary + 2)
              const data = frame
                .split('\n')
                .filter((line) => line.startsWith('data:'))
                .map((line) => line.slice(5).trim())
                .join('\n')

              if (data && handleUpdate(JSON.parse(data))) {
                controller.abort()
                return
              }
              boundary = buffer.indexOf('\n\n')
            }
          }
        } catch (err) {
          if (controller.signal.aborted) return
          console.error("Error en el stream de estado:", err)
        }
        await new Promise((resolve) => setTimeout(resolve, 2000))
      }
    }

    readStream()
    return () => controller.abort()
  }, [isTracking, orderID, authToken, loadOrder, handleSessionExpired])

  // 3. Temporizador regresivo de la reserva
  useEffect(() => {
    if (view !== 'CHECKOUT') return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [view])

  const remainingMs = expiresAt ? Math.max(0, expiresAt - now) : 0

  // 4. PAGAR: guardamos los asistentes, pedimos el init_point y abrimos Mercado Pago
  const changeAttendee = (index, field) => (event) => {
    const { value } = event.target
    setAttendees((prev) => prev.map((attendee, i) => (i === index ? { ...attendee, [field]: value } : attendee)))
    setAttendeeErrors((prev) => ({ ...prev, [attendeeKey(index, field)]: undefined }))
  }

  const handlePay = async () => {
    setCheckoutError('')
    const errors = validateAttendees(attendees)
    setAttendeeErrors(errors)
    const firstError = Object.keys(errors)[0]
    if (firstError) {
      document.getElementById(`att-${firstError}`)?.focus()
      return
    }

    setCheckoutLoading(true)
    // Abrimos la ventana dentro del click para que el navegador no la bloquee;
    // así esta pestaña sigue escuchando el SSE y pasa sola a las entradas.
    const paymentWindow = window.open('', '_blank')
    const slots = attendeeSlots(order)

    try {
      const res = await fetch(`${API_URL}/orders/${orderID}/checkout`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${authToken}` },
        body: JSON.stringify({
          asistentes: slots.map((slot, i) => ({ sector_id: slot.sector_id, nombre: attendees[i].nombre, dni: attendees[i].dni }))
        })
      })
      if (res.status === 401) {
        paymentWindow?.close()
        handleSessionExpired()
        return
      }
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        if (data.field?.startsWith('asistentes.')) {
          setAttendeeErrors({ [data.field]: data.error })
          paymentWindow?.close()
          return
        }
        if (res.status === 409) loadOrder(orderID) // Expiró o cambió de estado
        throw new Error(data.error || 'No se pudo iniciar el pago')
      }

      if (paymentWindow) {
        paymentWindow.opener = null
        paymentWindow.location.href = data.init_point
      } else {
        window.location.href = data.init_point
      }
    } catch (err) {
      paymentWindow?.close()
      setCheckoutError(err instanceof TypeError ? 'No se pudo conectar con el servidor' : err.message)
    } finally {
      setCheckoutLoading(false)
    }
  }

  // 5. VER EL EVENTO: armar el carrito
  // Refresca el stock de los sectores (el de la Home puede tener unos minutos) y ajusta el carrito.
  const refreshEvent = (eventID) =>
    fetch(`${API_URL}/events/${eventID}`)
      .then((res) => (res.ok ? res.json() : null))
      .then((evt) => {
        if (!evt) return
        setSelectedEvent(evt)
        setCart((prev) => clampCart(evt, prev))
      })
      .catch((err) => console.error('Error actualizando el evento:', err))

  const openEvent = (evt) => {
    setBuyError('')
    setSelectedEvent(evt)
    // Con un solo sector disponible arrancamos con 1 entrada: un clic menos.
    const available = evt.sectores.filter((sector) => sector.stock_disponible > 0)
    setCart(available.length === 1 ? { [available[0].id]: 1 } : {})
    setView('EVENT')
    window.scrollTo(0, 0)
    refreshEvent(evt.id)
  }

  const changeQuantity = (sector, delta) => {
    setBuyError('')
    setCart((prev) => clampCart(selectedEvent, { ...prev, [sector.id]: (prev[sector.id] || 0) + delta }))
  }

  // 6. COMPRAR
  const handleBuy = async () => {
    const items = selectedEvent.sectores
      .filter((sector) => cart[sector.id] > 0)
      .map((sector) => ({ sector_id: sector.id, nombre: sector.nombre, precio: sector.precio, cantidad: cart[sector.id] }))
    if (items.length === 0) return
    setBuyError('')
    setBuying(true)

    try {
      const res = await fetch(`${API_URL}/events/${selectedEvent.id}/comprar`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${authToken}` },
        body: JSON.stringify({ items: items.map(({ sector_id, cantidad }) => ({ sector_id, cantidad })) })
      })
      if (res.status === 401) {
        handleSessionExpired()
        return
      }
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        // 409 = se agotó mientras elegía; 400 = cambió el límite o un sector: refrescamos para que lo vea.
        if (res.status === 409 || res.status === 400) refreshEvent(selectedEvent.id)
        throw new Error(data.error || 'No se pudo iniciar la compra')
      }

      localStorage.setItem(PENDING_ORDER_KEY, String(data.order_id))
      setOrder(null)
      setAttendees([])
      setAttendeeErrors({})
      setError(null)
      setExpiresAt(null)
      setCheckoutError('')
      setOrderID(data.order_id)
      setStatus('pendiente')
      setView('QUEUE')
    } catch (err) {
      setBuyError(err instanceof TypeError ? 'No se pudo conectar con el servidor' : err.message)
    } finally {
      setBuying(false)
    }
  }

  // --- RENDERS ---

  // La home ocupa todo el ancho con la imagen de fondo; la grilla sigue centrada adentro.
  const renderHome = () => (
    <div className="home-page">
      <div className="grid-container">
        <div className="hero-section">
          <h1>OLAVARRÍA <span className="highlight">TICKETS</span></h1>
          <p>Los mejores eventos, asegurados con tecnología de alta frecuencia.</p>
        </div>

        {events.length === 0 && (
          <p className="events-empty">Todavía no hay eventos publicados. ¡Volvé pronto!</p>
        )}

        <div className="events-grid">
          {events.map((evt, index) => (
            <div key={evt.id} className="event-card">
              <div className="card-image">
                <img src={evt.portada_url || EVENT_IMAGES[index % EVENT_IMAGES.length]} alt={evt.nombre} />
                <div className="stock-badge">
                  {evt.StockDisponible > 0 ? `${evt.StockDisponible} Disp.` : "SOLD OUT"}
                </div>
              </div>
              <div className="card-content">
                <h3>{evt.nombre}</h3>
                <div className="card-info">
                  <span><FaCalendarAlt/> Hoy</span>
                  <span><FaMapMarkerAlt/> Centro</span>
                  <span><FaTicketAlt/> {evt.sectores?.length > 1 ? 'Desde ' : ''}{formatPrice(minPrice(evt))}</span>
                </div>
                <button 
                  className="buy-btn"
                  disabled={evt.StockDisponible === 0}
                  onClick={() => openEvent(evt)}
                >
                  {evt.StockDisponible > 0 ? "VER ENTRADAS" : "AGOTADO"}
                </button>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )

  const renderEvent = () => {
    const evt = selectedEvent
    const limite = evt.limite_tickets_por_orden
    const totalQty = cartQuantity(cart)
    const totalPrice = evt.sectores.reduce((sum, s) => sum + (cart[s.id] || 0) * s.precio, 0)
    const lines = evt.sectores.filter((s) => cart[s.id] > 0)
    const soldOut = evt.sectores.every((s) => s.stock_disponible === 0)

    return (
      <div className="event-page">
        <button type="button" className="link-btn admin-back" onClick={goHome}>
          <FaArrowLeft aria-hidden="true" /> Volver a los eventos
        </button>

        <section className="event-panel" aria-labelledby="event-title">
          {evt.portada_url && <img className="event-cover" src={evt.portada_url} alt="" />}
          <header>
            <h1 id="event-title">{evt.nombre}</h1>
            <div className="card-info">
              <span><FaCalendarAlt/> Hoy</span>
              <span><FaMapMarkerAlt/> Centro</span>
              {evt.organizador?.nombre && <span>{evt.organizador.nombre}</span>}
            </div>
          </header>

          {evt.mapa_url && (
            <figure className="venue-map">
              <a href={evt.mapa_url} target="_blank" rel="noreferrer" title="Ver el mapa en tamaño completo">
                <img src={evt.mapa_url} alt={`Mapa de sectores de ${evt.nombre}`} />
              </a>
              <figcaption>Mapa de sectores · tocá para ampliarlo</figcaption>
            </figure>
          )}

          <h2 className="sector-picker-title" id="sectores-title">Elegí tus entradas</h2>
          <ul className="sector-picker" aria-labelledby="sectores-title">
            {evt.sectores.map((s) => {
              const agotado = s.stock_disponible === 0
              const qty = cart[s.id] || 0
              return (
                <li key={s.id} className={`sector-option${qty > 0 ? ' selected' : ''}${agotado ? ' sold-out' : ''}`}>
                  <span className="sector-option-name" id={`sector-${s.id}-name`}>{s.nombre}</span>
                  <span className="sector-option-stock">
                    {agotado ? 'Agotado' : s.stock_disponible <= 20 ? `¡Últimas ${s.stock_disponible}!` : 'Disponible'}
                  </span>
                  <span className="sector-option-price">{formatPrice(s.precio)}</span>
                  <div className="qty-stepper" role="group" aria-labelledby={`sector-${s.id}-name`}>
                    <button
                      type="button"
                      onClick={() => changeQuantity(s, -1)}
                      disabled={qty === 0}
                      aria-label={`Quitar una entrada de ${s.nombre}`}
                    >
                      <FaMinus aria-hidden="true" />
                    </button>
                    <output aria-live="polite" aria-label={`Entradas de ${s.nombre}`}>{qty}</output>
                    <button
                      type="button"
                      onClick={() => changeQuantity(s, 1)}
                      disabled={agotado || qty >= s.stock_disponible || totalQty >= limite}
                      aria-label={`Agregar una entrada de ${s.nombre}`}
                    >
                      <FaPlus aria-hidden="true" />
                    </button>
                  </div>
                </li>
              )
            })}
          </ul>

          {soldOut ? (
            <p className="login-error" role="status">Este evento está agotado.</p>
          ) : (
            <>
              <p className="form-notice qty-hint" aria-live="polite">
                {totalQty >= limite
                  ? `Llegaste al máximo de ${ticketsLabel(limite)} por compra.`
                  : `Podés llevar hasta ${ticketsLabel(limite)} por compra, sumando todos los sectores.`}
              </p>

              <div className="cart" aria-labelledby="cart-title">
                <h2 id="cart-title">Tu carrito</h2>
                {lines.length === 0 ? (
                  <p className="form-notice">Todavía no agregaste entradas.</p>
                ) : (
                  <ul className="cart-lines">
                    {lines.map((s) => (
                      <li key={s.id}>
                        <span>{cart[s.id]} × {s.nombre}</span>
                        <span>{formatPrice(cart[s.id] * s.precio)}</span>
                      </li>
                    ))}
                  </ul>
                )}
                <div className="purchase-total">
                  <span>Total{totalQty > 0 ? ` · ${ticketsLabel(totalQty)}` : ''}</span>
                  <b>{totalQty > 0 ? formatPrice(totalPrice) : '—'}</b>
                </div>
              </div>

              {buyError && <p className="login-error" role="alert">{buyError}</p>}

              <button type="button" className="buy-btn" onClick={handleBuy} disabled={totalQty === 0 || buying}>
                {buying ? 'PROCESANDO...' : totalQty > 0 ? 'COMPRAR' : 'AGREGÁ ENTRADAS'}
              </button>
            </>
          )}
        </section>
      </div>
    )
  }

  const renderQueue = () => (
    <div className="queue-container">
      <div className="queue-box">
        {status === 'rechazada' || status === 'expirada' ? (
          <div className="queue-error">
            <h1>{status === 'expirada' ? '⌛' : '❌'} {error}</h1>
            <button onClick={goHome} className="back-btn">Volver al Inicio</button>
          </div>
        ) : (
          <>
            <div className="spinner-ring"></div>
            <h2>Estás en la Fila Virtual</h2>
            <p className="subtitle">No cierres esta página</p>
            
            <div className="queue-info">
              <p><FaUsers/> Usuarios en línea comprando para: <b>{order?.evento.nombre ?? selectedEvent?.nombre}</b></p>
            </div>
            <p className="mini-text">Orden ID: {orderID}</p>
          </>
        )}
      </div>
    </div>
  )

  const renderCheckout = () => {
    const expired = remainingMs <= 0
    const slots = attendeeSlots(order)
    const attendeeError = (key) =>
      attendeeErrors[key] && <span id={`att-${key}-error`} className="field-error">{attendeeErrors[key]}</span>
    const attendeeInput = (i, field) => {
      const key = attendeeKey(i, field)
      return {
        id: `att-${key}`,
        value: attendees[i]?.[field] ?? '',
        onChange: changeAttendee(i, field),
        'aria-invalid': attendeeErrors[key] ? 'true' : undefined,
        'aria-describedby': attendeeErrors[key] ? `att-${key}-error` : undefined
      }
    }

    return (
      <div className="queue-container">
        <div className="queue-box checkout-box">
          <h2>¡Entradas reservadas!</h2>
          <p className="subtitle">Completá los datos y el pago antes de que expire tu reserva</p>

          <div className={`countdown ${remainingMs < 60000 ? 'urgent' : ''}`}>
            <FaClock/> <span>{expired ? '00:00' : formatCountdown(remainingMs)}</span>
          </div>

          <div className="checkout-summary">
            <h3>{order?.evento.nombre ?? `Orden #${orderID}`}</h3>
            {order && (
              <>
                <ul className="cart-lines">
                  {order.items.map((item) => (
                    <li key={item.sector_id}>
                      <span>{item.cantidad} × {item.sector}</span>
                      <span>{formatPrice(item.precio_unitario * item.cantidad)}</span>
                    </li>
                  ))}
                </ul>
                <p>{ticketsLabel(slots.length)} · <b>{formatPrice(orderTotal(order))}</b></p>
              </>
            )}
            <p className="mini-text">Orden ID: {orderID}</p>
          </div>

          {expired ? (
            <p className="checkout-note">Tu reserva expiró. Liberando las entradas...</p>
          ) : order ? (
            <form
              className="attendees"
              noValidate
              onSubmit={(event) => {
                event.preventDefault()
                handlePay()
              }}
            >
              <h3>¿Quiénes van?</h3>
              <p className="form-notice">Cada entrada es personal: en el ingreso se controla el DNI de quien la usa.</p>
              {slots.map((slot, i) => (
                <fieldset key={i} className="attendee">
                  <legend>Entrada {i + 1} · {slot.sector}</legend>
                  <div className="form-field">
                    <label htmlFor={`att-${attendeeKey(i, 'nombre')}`}>Nombre y apellido</label>
                    <input {...attendeeInput(i, 'nombre')} maxLength={100} autoComplete={i === 0 ? 'name' : 'off'} />
                    {attendeeError(attendeeKey(i, 'nombre'))}
                  </div>
                  <div className="form-field">
                    <label htmlFor={`att-${attendeeKey(i, 'dni')}`}>DNI</label>
                    <input {...attendeeInput(i, 'dni')} maxLength={15} inputMode="numeric" autoComplete="off" />
                    {attendeeError(attendeeKey(i, 'dni'))}
                  </div>
                </fieldset>
              ))}

              <button type="submit" className="mp-btn" disabled={checkoutLoading}>
                {checkoutLoading ? 'CONECTANDO...' : 'PAGAR CON MERCADO PAGO'}
              </button>
              <p className="checkout-note">
                Se abrirá Mercado Pago en una nueva ventana. Esta pantalla se actualizará sola cuando se acredite el pago.
              </p>
            </form>
          ) : (
            <p className="checkout-note">Cargando tu orden...</p>
          )}
          {checkoutError && <p className="login-error" role="alert">{checkoutError}</p>}
        </div>
      </div>
    )
  }

  const renderTicket = () => {
    const tickets = order?.status === 'confirmada' ? order.tickets : []
    return (
      <div className="ticket-container">
        <div className="success-card">
          <div className="success-header">
            <h1>¡CONFIRMADO!</h1>
            <p>{tickets.length > 0 ? `Tus ${ticketsLabel(tickets.length)} están listas` : 'Cargando tus entradas...'}</p>
          </div>

          {order && <h3 className="ticket-event">{order.evento.nombre}</h3>}
          <ul className="ticket-list">
            {tickets.map((ticket) => (
              <li key={ticket.id} className={`ticket-item${ticket.ingresado ? ' used' : ''}`}>
                <div className="qr-section">
                  <QRCodeCanvas value={ticket.codigo} size={160} />
                </div>
                <div className="ticket-data">
                  <h4>{ticket.nombre}</h4>
                  <p>DNI <b>{ticket.dni}</b></p>
                  <p>{ticket.sector} · Entrada #{ticket.id}</p>
                  {ticket.ingresado ? (
                    <div className="status-tag used-tag">
                      INGRESÓ{ticket.ingresado_en ? ` ${timeFormatter.format(new Date(ticket.ingresado_en))}` : ''}
                    </div>
                  ) : (
                    <div className="status-tag">PAGADO</div>
                  )}
                </div>
              </li>
            ))}
          </ul>
          <p className="mini-text">Orden #{orderID} · También te enviamos las entradas en PDF por email.</p>

          <button onClick={goHome} className="download-btn">
            <FaArrowLeft/> Volver a Comprar
          </button>
        </div>
      </div>
    )
  }

  // La tienda (login, home, fila, checkout, ticket) sigue manejándose con `view`.
  // Un portero logueado no la ve: siempre vuelve a su app de puerta.
  const renderStore = () => rol === 'portero' ? <Navigate to={SCANNER_APP_PATH} replace /> : (
    <>
      {view === 'LOGIN' && (
        <Login
          notice={loginNotice}
          onAuthenticated={handleAuthenticated}
          onGoToRegister={goTo('REGISTER')}
          onGoToForgotPassword={goTo('FORGOT_PASSWORD')}
        />
      )}
      {view === 'REGISTER' && (
        <Register onAuthenticated={handleAuthenticated} onGoToLogin={goTo('LOGIN')} />
      )}
      {view === 'FORGOT_PASSWORD' && <ForgotPassword onGoToLogin={goTo('LOGIN')} />}
      {view === 'RESET_PASSWORD' && (
        <ResetPassword
          token={resetToken}
          onDone={handlePasswordReset}
          onGoToForgotPassword={goTo('FORGOT_PASSWORD')}
        />
      )}
      {view === 'HOME' && renderHome()}
      {view === 'EVENT' && selectedEvent && renderEvent()}
      {view === 'QUEUE' && renderQueue()}
      {view === 'CHECKOUT' && renderCheckout()}
      {view === 'TICKET' && renderTicket()}
    </>
  )

  // La app de puerta es pantalla completa: sin navbar ni el resto de la tienda.
  if (location.pathname.startsWith(SCANNER_APP_PATH)) {
    return (
      <ProtectedRoute rol={rol} allowedRoles={SCANNER_ROLES}>
        <Suspense fallback={<p className="admin-empty">Cargando escáner...</p>}>
          <ScannerApp authToken={authToken} onUnauthorized={handleLogout} onLogout={handleLogout} />
        </Suspense>
      </ProtectedRoute>
    )
  }

  return (
    <div className="app-wrapper">
      <nav className="navbar">
        <div className="logo" onClick={goHome}>OLA<b>TICKET</b></div>
        {authToken && (
          <div className="nav-account">
            {canAccessPanel(rol) && (
              <Link to="/admin" className={`panel-btn ${inPanel ? 'active' : ''}`}>Mi Panel</Link>
            )}
            <div className="user">👤 {userID}</div>
            <button className="logout-btn" onClick={handleLogout}>
              <span className="label-long">Cerrar Sesión</span>
              <span className="label-short">Salir</span>
            </button>
          </div>
        )}
      </nav>
      <main>
        <Routes>
          <Route path="/admin" element={<ProtectedRoute rol={rol} allowedRoles={PANEL_ROLES} />}>
            <Route index element={<AdminDashboard authToken={authToken} rol={rol} userID={userID} onUnauthorized={handleLogout} />} />
            <Route path="eventos/nuevo" element={<EventForm key="nuevo" authToken={authToken} rol={rol} onUnauthorized={handleLogout} />} />
            <Route path="eventos/:id/editar" element={<EventForm authToken={authToken} rol={rol} onUnauthorized={handleLogout} />} />
            <Route path="eventos/:id/escanear" element={
              <Suspense fallback={<p className="admin-empty">Cargando escáner...</p>}>
                <EventScanner authToken={authToken} onUnauthorized={handleLogout} />
              </Suspense>
            } />
          </Route>
          <Route path="*" element={renderStore()} />
        </Routes>
      </main>
    </div>
  )
}

export default App
