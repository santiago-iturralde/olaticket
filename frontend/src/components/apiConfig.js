// URL base del backend. VITE_API_URL se fija al compilar (en Vercel, en las variables del
// proyecto); sin ella se usa el backend local. Se quita la "/" final para no armar "//api".
export const API_URL = (import.meta.env.VITE_API_URL || 'http://localhost:8080').replace(/\/+$/, '')
