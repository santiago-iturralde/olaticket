import { useState } from 'react'

import { API_URL } from './apiConfig'

function ForgotPassword({ onGoToLogin }) {
  const [email, setEmail] = useState('')
  const [sentMessage, setSentMessage] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (event) => {
    event.preventDefault()
    setError('')
    setLoading(true)
    try {
      const res = await fetch(`${API_URL}/api/auth/forgot-password`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email })
      })
      const data = await res.json()
      if (!res.ok) throw new Error(data.error || 'No se pudo procesar la solicitud')
      setSentMessage(data.mensaje)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login-container">
      <form className="login-card" onSubmit={handleSubmit}>
        <h1>Recuperar contraseña</h1>

        {sentMessage ? (
          <p className="form-success" role="status">{sentMessage}</p>
        ) : (
          <>
            <p className="form-notice">
              Ingresá el correo de tu cuenta y te enviaremos un enlace para crear una nueva contraseña.
              El enlace vence en 15 minutos.
            </p>
            <label htmlFor="forgot-email">Correo electrónico</label>
            <input
              id="forgot-email"
              type="email"
              autoComplete="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              required
            />
            {error && <p className="login-error" role="alert">{error}</p>}
            <button className="buy-btn" type="submit" disabled={loading}>
              {loading ? 'ENVIANDO...' : 'Enviar enlace'}
            </button>
          </>
        )}

        <p className="auth-switch">
          <button type="button" className="link-btn" onClick={onGoToLogin}>Volver a iniciar sesión</button>
        </p>
      </form>
    </div>
  )
}

export default ForgotPassword
