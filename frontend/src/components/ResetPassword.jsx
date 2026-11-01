import { useState } from 'react'
import PasswordInput from './PasswordInput'
import PasswordRules from './PasswordRules'
import { isPasswordValid } from './passwordPolicy'

import { API_URL } from './apiConfig'

// Vista a la que llega el enlace del email (/?reset_token=...).
function ResetPassword({ token, onDone, onGoToForgotPassword }) {
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [tokenInvalid, setTokenInvalid] = useState(!token)
  const [loading, setLoading] = useState(false)

  const passwordValid = isPasswordValid(password)
  const passwordsMatch = password === confirmPassword
  const showMismatch = confirmPassword !== '' && !passwordsMatch

  const handleSubmit = async (event) => {
    event.preventDefault()
    setError('')
    if (!passwordValid || !passwordsMatch) return

    setLoading(true)
    try {
      const res = await fetch(`${API_URL}/api/auth/reset-password`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token, password })
      })
      const data = await res.json()
      if (!res.ok) {
        if (data.field === 'token') setTokenInvalid(true)
        throw new Error(data.error || 'No se pudo restablecer la contraseña')
      }
      onDone(data.mensaje)
    } catch (err) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  if (tokenInvalid) {
    return (
      <div className="login-container">
        <div className="login-card">
          <h1>Enlace no válido</h1>
          <p className="login-error" role="alert">
            {error || 'El enlace para restablecer la contraseña es inválido o expiró.'}
          </p>
          <button className="buy-btn" type="button" onClick={onGoToForgotPassword}>Pedir un enlace nuevo</button>
        </div>
      </div>
    )
  }

  return (
    <div className="login-container">
      <form className="login-card" onSubmit={handleSubmit}>
        <h1>Nueva contraseña</h1>

        <label htmlFor="reset-password">Contraseña nueva</label>
        <PasswordInput
          id="reset-password"
          autoComplete="new-password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          aria-describedby="reset-password-rules"
          required
        />
        <PasswordRules id="reset-password-rules" password={password} />

        <label htmlFor="reset-confirm">Confirmar contraseña</label>
        <PasswordInput
          id="reset-confirm"
          autoComplete="new-password"
          value={confirmPassword}
          onChange={(event) => setConfirmPassword(event.target.value)}
          aria-invalid={showMismatch}
          required
        />
        {showMismatch && <span className="field-error">Las contraseñas no coinciden</span>}

        {error && <p className="login-error" role="alert">{error}</p>}
        <button className="buy-btn" type="submit" disabled={loading || !passwordValid || !passwordsMatch}>
          {loading ? 'GUARDANDO...' : 'Guardar contraseña'}
        </button>
      </form>
    </div>
  )
}

export default ResetPassword
