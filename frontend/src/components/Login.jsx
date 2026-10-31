import { useRef, useState } from 'react'
import ReCAPTCHA from 'react-google-recaptcha'
import PasswordInput from './PasswordInput'
import { RECAPTCHA_SITE_KEY } from './recaptcha'

const API_URL = import.meta.env.VITE_API_URL

function Login({ onAuthenticated, onGoToRegister, onGoToForgotPassword, notice }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [captchaToken, setCaptchaToken] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const captchaRef = useRef(null)

  const handleSubmit = async (event) => {
    event.preventDefault()
    setError('')
    if (!captchaToken) {
      setError('Completá la verificación "No soy un robot"')
      return
    }

    setLoading(true)
    try {
      const res = await fetch(`${API_URL}/api/auth/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password, recaptcha_token: captchaToken })
      })
      const data = await res.json()
      if (!res.ok) throw new Error(data.error || 'No se pudo iniciar sesión')

      setPassword('')
      onAuthenticated(data)
    } catch (err) {
      setError(err.message)
      // Cada token de reCAPTCHA sirve una sola vez: hay que volver a resolverlo.
      captchaRef.current?.reset()
      setCaptchaToken('')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login-container">
      <form className="login-card" onSubmit={handleSubmit}>
        <h1>Iniciar sesión</h1>
        {notice && <p className="form-success" role="status">{notice}</p>}

        <label htmlFor="login-email">Correo electrónico</label>
        <input
          id="login-email"
          type="email"
          autoComplete="email"
          value={email}
          onChange={(event) => setEmail(event.target.value)}
          required
        />

        <label htmlFor="login-password">Contraseña</label>
        <PasswordInput
          id="login-password"
          autoComplete="current-password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          required
        />

        <button type="button" className="link-btn align-end" onClick={onGoToForgotPassword}>
          ¿Olvidaste tu contraseña?
        </button>

        <div className="captcha-wrapper">
          <ReCAPTCHA
            ref={captchaRef}
            sitekey={RECAPTCHA_SITE_KEY}
            theme="dark"
            onChange={(token) => setCaptchaToken(token || '')}
            onExpired={() => setCaptchaToken('')}
          />
        </div>

        {error && <p className="login-error" role="alert">{error}</p>}
        <button className="buy-btn" type="submit" disabled={loading}>
          {loading ? 'INGRESANDO...' : 'Ingresar'}
        </button>

        <p className="auth-switch">
          ¿No tienes una cuenta?{' '}
          <button type="button" className="link-btn" onClick={onGoToRegister}>Registrate ahora</button>
        </p>
      </form>
    </div>
  )
}

export default Login
