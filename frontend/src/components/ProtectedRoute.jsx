import { Navigate, Outlet } from 'react-router'

// Deja pasar solo si el rol del usuario está en allowedRoles; si no, vuelve a la home.
// Sin children renderiza las rutas hijas (<Outlet />).
function ProtectedRoute({ rol, allowedRoles, children }) {
  if (!allowedRoles.includes(rol)) {
    return <Navigate to="/" replace />
  }
  return children ?? <Outlet />
}

export default ProtectedRoute
