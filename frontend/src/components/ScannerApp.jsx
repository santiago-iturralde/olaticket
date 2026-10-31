import { useEffect, useRef, useState } from 'react'
import { Html5Qrcode } from 'html5-qrcode'
import { FaCamera, FaSignOutAlt } from 'react-icons/fa'
import ScanResult from './ScanResult'
import { validateTicket } from './scanApi'

const API_URL = import.meta.env.VITE_API_URL
const READER_ID = 'scanner-app-reader'
const EVENT_KEY = 'scanner_event_id'

// Un QR válido vuelve solo a la cámara; uno rechazado espera un toque para que no pase
// desapercibido. El mismo código no se vuelve a validar enseguida (la cámara lo sigue viendo).
const OK_DISMISS_MS = 2000
const SAME_CODE_COOLDOWN_MS = 4000

const storedEventID = () => {
  try {
    return Number(localStorage.getItem(EVENT_KEY)) || null
  } catch {
    return null
  }
}

// App de puerta para porteros (también la pueden usar organizadores y admins): pantalla
// completa, sin el panel. Se elige el evento y la cámara lee QRs de forma continua.
// La cámara solo se abre en HTTPS o en localhost (restricción del navegador).
function ScannerApp({ authToken, onUnauthorized, onLogout }) {
  const [events, setEvents] = useState(null)
  const [loadError, setLoadError] = useState('')
  const [eventID, setEventID] = useState(storedEventID)
  const [scanning, setScanning] = useState(false)
  const [starting, setStarting] = useState(false)
  const [cameraError, setCameraError] = useState('')
  const [result, setResult] = useState(null)
  const [admitted, setAdmitted] = useState(0)

  const scannerRef = useRef(null)
  // Lo que lee el callback de la cámara (se registra una sola vez, no ve el estado nuevo).
  const eventRef = useRef(eventID)
  const busyRef = useRef(false)
  const lastReadRef = useRef({ code: '', at: 0 })
  const dismissRef = useRef(null)

  useEffect(() => {
    const controller = new AbortController()
    fetch(`${API_URL}/api/admin/escaner/eventos`, {
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
        // Si el evento recordado ya no está (o es el único) se ajusta la selección.
        setEventID((current) => {
          const next = data.some((evt) => evt.id === current) ? current : data.length === 1 ? data[0].id : null
          eventRef.current = next
          return next
        })
      })
      .catch((err) => {
        if (!controller.signal.aborted) setLoadError(err.message)
      })
    return () => controller.abort()
  }, [authToken, onUnauthorized])

  // Al salir de la pantalla apagamos la cámara y el temporizador.
  useEffect(() => () => {
    clearTimeout(dismissRef.current)
    const scanner = scannerRef.current
    if (scanner?.isScanning) scanner.stop().catch(() => {})
  }, [])

  const dismiss = () => {
    clearTimeout(dismissRef.current)
    setResult(null)
    busyRef.current = false
  }

  const handleCode = async (codigo) => {
    const now = Date.now()
    const last = lastReadRef.current
    if (busyRef.current || !eventRef.current || (codigo === last.code && now - last.at < SAME_CODE_COOLDOWN_MS)) return
    busyRef.current = true
    lastReadRef.current = { code: codigo, at: now }

    const next = await validateTicket(authToken, eventRef.current, codigo)
    if (!next) {
      onUnauthorized()
      return
    }
    setResult(next)
    if (next.ok) {
      setAdmitted((n) => n + 1)
      dismissRef.current = setTimeout(dismiss, OK_DISMISS_MS)
    }
    // busyRef sigue en true hasta que se cierra el cartel: mientras tanto no se lee nada.
  }

  const startCamera = async () => {
    if (scannerRef.current?.isScanning) return
    setCameraError('')
    setStarting(true)
    try {
      scannerRef.current ??= new Html5Qrcode(READER_ID, { verbose: false })
      // Sin qrbox: lee en todo el cuadro (el visor es solo una guía visual).
      await scannerRef.current.start({ facingMode: 'environment' }, { fps: 12 }, handleCode, () => {})
      setScanning(true)
    } catch (err) {
      console.error('Error abriendo la cámara:', err)
      setCameraError(
        window.isSecureContext
          ? 'No se pudo abrir la cámara. Revisá que el navegador tenga permiso para usarla.'
          : 'La cámara solo funciona si la página se abre por HTTPS.'
      )
    } finally {
      setStarting(false)
    }
  }

  const selectEvent = (event) => {
    const id = Number(event.target.value) || null
    setEventID(id)
    eventRef.current = id
    lastReadRef.current = { code: '', at: 0 } // El mismo QR puede valer en otro evento
    try {
      if (id) localStorage.setItem(EVENT_KEY, String(id))
      else localStorage.removeItem(EVENT_KEY)
    } catch {
      // Sin almacenamiento solo se pierde recordar el evento
    }
    if (id) startCamera() // El cambio del select es un gesto del usuario: permite pedir la cámara
  }

  const selected = events?.find((evt) => evt.id === eventID)

  return (
    <div className="scanner-app">
      <header className="scanner-app-bar">
        <label htmlFor="scanner-app-event" className="sr-only">Evento</label>
        <select
          id="scanner-app-event"
          value={eventID ?? ''}
          onChange={selectEvent}
          disabled={!events || events.length === 0}
        >
          <option value="">{events === null ? 'Cargando eventos...' : events.length === 0 ? 'Sin eventos' : 'Elegí el evento'}</option>
          {events?.map((evt) => (
            <option key={evt.id} value={evt.id}>{evt.nombre}</option>
          ))}
        </select>
        <button type="button" className="scanner-app-logout" onClick={onLogout} aria-label="Cerrar sesión" title="Cerrar sesión">
          <FaSignOutAlt aria-hidden="true" />
        </button>
      </header>

      <main className="scanner-app-camera">
        <div id={READER_ID} className={`scanner-app-reader${scanning ? '' : ' idle'}`} />
        {scanning && (
          <>
            <div className="scanner-app-frame" aria-hidden="true" />
            <p className="scanner-app-hint" aria-live="polite">
              {selected ? `Apuntá al QR · ${selected.nombre}` : 'Elegí el evento para validar'}
            </p>
          </>
        )}
        {!scanning && (
          <div className="scanner-app-idle">
            <FaCamera aria-hidden="true" />
            {loadError ? (
              <p className="login-error" role="alert">{loadError}</p>
            ) : events?.length === 0 ? (
              <p>Tu productora no tiene eventos cargados.</p>
            ) : (
              <button type="button" className="buy-btn" onClick={startCamera} disabled={!eventID || starting}>
                {starting ? 'ABRIENDO CÁMARA...' : eventID ? 'ACTIVAR CÁMARA' : 'ELEGÍ EL EVENTO'}
              </button>
            )}
            {cameraError && <p className="login-error" role="alert">{cameraError}</p>}
          </div>
        )}
        <p className="scanner-app-counter" aria-live="polite">
          <b>{admitted}</b> {admitted === 1 ? 'ingreso' : 'ingresos'}
        </p>
      </main>

      {result && (
        <ScanResult
          result={result}
          buttonLabel={result.ok ? 'SIGUIENTE' : 'ENTENDIDO, SIGUIENTE'}
          onNext={dismiss}
        />
      )}
    </div>
  )
}

export default ScannerApp
