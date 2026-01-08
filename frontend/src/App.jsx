import { useState, useEffect } from 'react'
import { QRCodeCanvas } from 'qrcode.react' // <--- CORREGIDO AQUÍ
import { FaMapMarkerAlt, FaCalendarAlt, FaClock, FaTicketAlt, FaArrowLeft, FaUsers } from 'react-icons/fa'
import './App.css'

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

function App() {
  const [view, setView] = useState('HOME') 
  const [events, setEvents] = useState([])
  const [selectedEvent, setSelectedEvent] = useState(null)
  
  const [orderID, setOrderID] = useState(null)
  const [queuePos, setQueuePos] = useState(0)
  const [status, setStatus] = useState(null) 
  const [error, setError] = useState(null)
  
  const USER_ID = Math.floor(Math.random() * 90000) + 10000 

  // 1. CARGAR EVENTOS
  useEffect(() => {
    fetch('http://localhost:8080/events')
      .then(res => res.json())
      .then(data => setEvents(data))
      .catch(err => console.error("Error cargando eventos:", err))
  }, [])

  // 2. POLLING
  useEffect(() => {
    let interval = null
    if (view === 'QUEUE' && orderID) {
      interval = setInterval(checkStatus, 2000)
    }
    return () => clearInterval(interval)
  }, [view, orderID])

  const checkStatus = async () => {
    try {
      const res = await fetch(`http://localhost:8080/orders/${orderID}`)
      const data = await res.json()
      
      setStatus(data.status)
      setQueuePos(data.position) 

      if (data.status === 'confirmada') {
        setView('TICKET')
      } else if (data.status === 'rechazada') {
        setError(data.mensaje || "Agotado")
      }
    } catch (err) {
      console.error("Error polling:", err)
    }
  }

  // 3. COMPRAR
  const handleBuy = async (event) => {
    setSelectedEvent(event)
    setError(null)
    
    try {
      const res = await fetch(`http://localhost:8080/events/${event.id}/comprar`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ user_id: USER_ID })
      })
      
      if (!res.ok) throw new Error("Error al conectar")
      
      const data = await res.json()
      setOrderID(data.order_id)
      setStatus('pendiente')
      setView('QUEUE') 

    } catch (err) {
      alert("Error al intentar comprar: " + err.message)
    }
  }

  // --- RENDERS ---

  const renderHome = () => (
    <div className="grid-container">
      <div className="hero-section">
        <h1>OLAVARRÍA <span className="highlight">TICKETS</span></h1>
        <p>Los mejores eventos, asegurados con tecnología de alta frecuencia.</p>
      </div>

      <div className="events-grid">
        {events.map((evt, index) => (
          <div key={evt.id} className="event-card">
            <div className="card-image">
              <img src={EVENT_IMAGES[index % EVENT_IMAGES.length]} alt={evt.nombre} />
              <div className="stock-badge">
                {evt.StockDisponible > 0 ? `${evt.StockDisponible} Disp.` : "SOLD OUT"}
              </div>
            </div>
            <div className="card-content">
              <h3>{evt.nombre}</h3>
              <div className="card-info">
                <span><FaCalendarAlt/> Hoy</span>
                <span><FaMapMarkerAlt/> Centro</span>
              </div>
              <button 
                className="buy-btn"
                disabled={evt.StockDisponible === 0}
                onClick={() => handleBuy(evt)}
              >
                {evt.StockDisponible > 0 ? "COMPRAR ENTRADA" : "AGOTADO"}
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  )

  const renderQueue = () => (
    <div className="queue-container">
      <div className="queue-box">
        {status === 'rechazada' ? (
          <div className="queue-error">
            <h1>❌ {error}</h1>
            <button onClick={() => setView('HOME')} className="back-btn">Volver al Inicio</button>
          </div>
        ) : (
          <>
            <div className="spinner-ring"></div>
            <h2>Estás en la Fila Virtual</h2>
            <p className="subtitle">No cierres esta página</p>
            
            <div className="position-indicator">
              <span className="label">Tu posición actual:</span>
              <span className="number">#{queuePos > 0 ? queuePos : "..."}</span>
            </div>

            <div className="queue-info">
              <p><FaUsers/> Usuarios en línea comprando para: <b>{selectedEvent?.nombre}</b></p>
            </div>
            <p className="mini-text">Orden ID: {orderID}</p>
          </>
        )}
      </div>
    </div>
  )

  const renderTicket = () => (
    <div className="ticket-container">
      <div className="success-card">
        <div className="success-header">
          <h1>¡CONFIRMADO!</h1>
          <p>Tu entrada está lista</p>
        </div>
        
        <div className="qr-section">
          {/* CORREGIDO AQUÍ TAMBIÉN 👇 */}
          <QRCodeCanvas value={`TICKET-${orderID}-VALID-USER-${USER_ID}`} size={180} />
        </div>

        <div className="ticket-data">
          <h3>{selectedEvent?.nombre}</h3>
          <p>Usuario: <b>#{USER_ID}</b></p>
          <p>Orden: <b>#{orderID}</b></p>
          <div className="status-tag">PAGADO</div>
        </div>

        <button onClick={() => setView('HOME')} className="download-btn">
          <FaArrowLeft/> Volver a Comprar
        </button>
      </div>
    </div>
  )

  return (
    <div className="app-wrapper">
      <nav className="navbar">
        <div className="logo" onClick={() => setView('HOME')}>OLA<b>TICKET</b></div>
        <div className="user">👤 {USER_ID}</div>
      </nav>
      <main>
        {view === 'HOME' && renderHome()}
        {view === 'QUEUE' && renderQueue()}
        {view === 'TICKET' && renderTicket()}
      </main>
    </div>
  )
}

export default App