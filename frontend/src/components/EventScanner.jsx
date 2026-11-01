import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router'
import { Html5Qrcode } from 'html5-qrcode'
import { FaArrowLeft, FaCamera } from 'react-icons/fa'
import ScanResult from './ScanResult'
import { validateTicket } from './scanApi'

import { API_URL } from './apiConfig'
const READER_ID = 'qr-reader'

// Escáner de accesos para la puerta. Usa la cámara trasera del celular (html5-qrcode) y
// también acepta un lector USB/Bluetooth, que "tipea" el código en el campo manual.
// La cámara solo se abre en HTTPS o en localhost (restricción del navegador).
function EventScanner({ authToken, onUnauthorized }) {
  const { id } = useParams()
  const [eventName, setEventName] = useState('')
  const [loadError, setLoadError] = useState('')
  const [scanning, setScanning] = useState(false)
  const [starting, setStarting] = useState(false)
  const [cameraError, setCameraError] = useState('')
  const [checking, setChecking] = useState(false)
  const [result, setResult] = useState(null)
  const [manualCode, setManualCode] = useState('')
  const [admitted, setAdmitted] = useState(0)
  const [cameraUsed, setCameraUsed] = useState(false) // Después de cada resultado se reactiva la cámara

  const scannerRef = useRef(null)
  const busyRef = useRef(false) // La cámara puede disparar varias lecturas del mismo QR seguidas

  useEffect(() => {
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
        setEventName(data.nombre)
      })
      .catch((err) => {
        if (!controller.signal.aborted) setLoadError(err.message)
      })
    return () => controller.abort()
  }, [id, authToken, onUnauthorized])

  // Al salir de la pantalla apagamos la cámara.
  useEffect(() => () => {
    const scanner = scannerRef.current
    if (scanner?.isScanning) scanner.stop().catch(() => {})
  }, [])

  const validate = async (codigo) => {
    setChecking(true)
    const next = await validateTicket(authToken, id, codigo)
    setChecking(false)
    busyRef.current = false
    if (!next) {
      onUnauthorized()
      return
    }
    setResult(next)
    if (next.ok) setAdmitted((n) => n + 1)
  }

  const stopCamera = async () => {
    const scanner = scannerRef.current
    if (scanner?.isScanning) await scanner.stop().catch(() => {})
    setScanning(false)
  }

  const startCamera = async () => {
    setResult(null)
    setCameraError('')
    setStarting(true)
    try {
      scannerRef.current ??= new Html5Qrcode(READER_ID, { verbose: false })
      await scannerRef.current.start(
        { facingMode: 'environment' },
        { fps: 10, qrbox: (w, h) => { const side = Math.floor(Math.min(w, h) * 0.75); return { width: side, height: side } } },
        async (decodedText) => {
          if (busyRef.current) return
          busyRef.current = true
          await stopCamera() // Detenemos la cámara antes de validar: un QR, un resultado
          validate(decodedText)
        },
        () => {} // Cuadros sin QR: no es un error
      )
      setScanning(true)
      setCameraUsed(true)
    } catch (err) {
      console.error('Error abriendo la cámara:', err)
      setCameraError(
        window.isSecureContext
          ? 'No se pudo abrir la cámara. Revisá que el navegador tenga permiso para usarla.'
          : 'El navegador solo permite la cámara en HTTPS (o en localhost). Podés usar un lector de QR USB en el campo de abajo.'
      )
    } finally {
      setStarting(false)
    }
  }

  const handleManualSubmit = (event) => {
    event.preventDefault()
    const codigo = manualCode.trim()
    if (!codigo || busyRef.current) return
    busyRef.current = true
    setManualCode('')
    validate(codigo)
  }

  const next = () => {
    setResult(null)
    if (cameraUsed) startCamera()
  }

  return (
    <div className="admin-page scanner-page">
      <Link to="/admin" className="link-btn admin-back"><FaArrowLeft aria-hidden="true" /> Volver al panel</Link>

      <header className="admin-header">
        <div>
          <h1>Control de acceso</h1>
          <p className="admin-subtitle">{loadError || eventName || 'Cargando evento...'}</p>
        </div>
        <p className="scanner-counter" aria-live="polite">
          <b>{admitted}</b> {admitted === 1 ? 'ingreso' : 'ingresos'} en esta sesión
        </p>
      </header>

      {!loadError && (
        <section className="scanner-panel" aria-label="Escáner">
          <div id={READER_ID} className={`scanner-reader${scanning ? '' : ' idle'}`} />
          {!scanning && !result && (
            <div className="scanner-idle">
              <FaCamera aria-hidden="true" />
              <button type="button" className="buy-btn" onClick={startCamera} disabled={starting || checking}>
                {starting ? 'ABRIENDO CÁMARA...' : checking ? 'VALIDANDO...' : 'ACTIVAR CÁMARA'}
              </button>
            </div>
          )}
          {scanning && (
            <button type="button" className="logout-btn scanner-stop" onClick={stopCamera}>Pausar cámara</button>
          )}
          {cameraError && <p className="login-error" role="alert">{cameraError}</p>}

          <form className="scanner-manual" onSubmit={handleManualSubmit}>
            <label htmlFor="scanner-code">Código (lector USB o carga manual)</label>
            <div>
              <input
                id="scanner-code"
                value={manualCode}
                onChange={(event) => setManualCode(event.target.value)}
                placeholder="OLT-123-..."
                autoComplete="off"
                autoCapitalize="characters"
                spellCheck="false"
              />
              <button type="submit" className="logout-btn" disabled={!manualCode.trim() || checking}>Validar</button>
            </div>
          </form>
        </section>
      )}

      {result && (
        <ScanResult result={result} buttonLabel={cameraUsed ? 'ESCANEAR SIGUIENTE' : 'CONTINUAR'} onNext={next} />
      )}
    </div>
  )
}

export default EventScanner
