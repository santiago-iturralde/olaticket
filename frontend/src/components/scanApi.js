import { API_URL } from './apiConfig'

const timeFormatter = new Intl.DateTimeFormat('es-AR', { hour: '2-digit', minute: '2-digit' })

// Lo que muestra la pantalla según la respuesta de POST /api/admin/eventos/:id/scan.
const resultFromResponse = (status, data) => {
  const ticket = data.ticket
  switch (status) {
    case 200:
      return { ok: true, title: 'ADELANTE', detail: 'Controlá que el DNI coincida', ticket }
    case 409:
      return {
        ok: false,
        title: 'YA INGRESÓ',
        detail: ticket?.ingresado_en ? `Esta entrada se usó a las ${timeFormatter.format(new Date(ticket.ingresado_en))}` : data.error,
        ticket
      }
    case 403:
      return { ok: false, title: 'NO PAGADA', detail: data.error, ticket }
    default:
      return { ok: false, title: 'RECHAZADA', detail: data.error || 'Entrada inválida' }
  }
}

// Valida un código leído contra el evento. Devuelve el resultado a mostrar, o null si la
// sesión venció (401). Nunca rechaza: un error de red también es un resultado (rojo).
export const validateTicket = async (authToken, eventID, codigo) => {
  let result
  try {
    const res = await fetch(`${API_URL}/api/admin/eventos/${eventID}/scan`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${authToken}` },
      body: JSON.stringify({ codigo })
    })
    if (res.status === 401) return null
    const data = await res.json().catch(() => ({}))
    result = res.status >= 500
      ? { ok: false, title: 'ERROR', detail: 'No se pudo validar. Volvé a escanear la entrada.' }
      : resultFromResponse(res.status, data)
  } catch {
    result = { ok: false, title: 'SIN CONEXIÓN', detail: 'No se pudo validar. Revisá la conexión y volvé a escanear.' }
  }
  navigator.vibrate?.(result.ok ? 120 : [80, 60, 80, 60, 200])
  return result
}
