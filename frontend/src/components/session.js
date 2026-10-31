// Roles con acceso al backoffice y a la app de puerta (deben coincidir con RequireRole en el
// backend). El portero solo escanea: no entra al panel ni a la tienda.
export const PANEL_ROLES = ['organizador', 'admin']
export const SCANNER_ROLES = ['portero', 'organizador', 'admin']
export const SCANNER_APP_PATH = '/escaner-app'

// Lee los claims del JWT sin verificar la firma: solo sirve para decidir qué mostrar.
// La autorización real la hace el backend en cada request.
export const decodeTokenClaims = (token) => {
  try {
    const payload = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')
    const json = decodeURIComponent(
      [...atob(payload)].map((c) => '%' + c.charCodeAt(0).toString(16).padStart(2, '0')).join('')
    )
    const claims = JSON.parse(json)
    if (claims.exp && claims.exp * 1000 < Date.now()) return null
    return claims
  } catch {
    return null
  }
}

export const canAccessPanel = (rol) => PANEL_ROLES.includes(rol)
