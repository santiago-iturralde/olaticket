import { useRef, useState } from 'react'
import ReCAPTCHA from 'react-google-recaptcha'
import PasswordInput from './PasswordInput'
import PasswordRules from './PasswordRules'
import { isPasswordValid } from './passwordPolicy'
import { RECAPTCHA_SITE_KEY } from './recaptcha'

const API_URL = import.meta.env.VITE_API_URL

const COUNTRIES = [
  { name: 'Argentina', dialCode: '+54' },
  { name: 'Bolivia', dialCode: '+591' },
  { name: 'Brasil', dialCode: '+55' },
  { name: 'Chile', dialCode: '+56' },
  { name: 'Colombia', dialCode: '+57' },
  { name: 'España', dialCode: '+34' },
  { name: 'Estados Unidos', dialCode: '+1' },
  { name: 'México', dialCode: '+52' },
  { name: 'Paraguay', dialCode: '+595' },
  { name: 'Perú', dialCode: '+51' },
  { name: 'Uruguay', dialCode: '+598' },
  { name: 'Otro', dialCode: '' },
]
const DIAL_CODES = [...new Set(COUNTRIES.map((c) => c.dialCode).filter(Boolean))]

const DOCUMENT_TYPES = [
  { value: 'DNI', label: 'DNI' },
  { value: 'PASAPORTE', label: 'Pasaporte' },
  { value: 'CI', label: 'Cédula de identidad' },
  { value: 'OTRO', label: 'Otro' },
]

const TODAY = new Date().toISOString().slice(0, 10)

const INITIAL_FORM = {
  nombre: '',
  apellido: '',
  email: '',
  pais_residencia: 'Argentina',
  dial_code: '+54',
  telefono: '',
  tipo_documento: 'DNI',
  numero_dni: '',
  fecha_nacimiento: '',
  password: '',
  confirm_password: '',
  acepta_suscripcion: false,
}

function Register({ onAuthenticated, onGoToLogin }) {
  const [form, setForm] = useState(INITIAL_FORM)
  const [fieldErrors, setFieldErrors] = useState({})
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [captchaToken, setCaptchaToken] = useState('')
  const captchaRef = useRef(null)

  const passwordValid = isPasswordValid(form.password)
  const passwordsMatch = form.password === form.confirm_password
  const showMismatch = form.confirm_password !== '' && !passwordsMatch

  const update = (field) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value
    setForm((prev) => ({ ...prev, [field]: value }))
    setFieldErrors((prev) => ({ ...prev, [field]: undefined }))
  }

  const handleCountryChange = (event) => {
    const country = COUNTRIES.find((c) => c.name === event.target.value)
    setForm((prev) => ({
      ...prev,
      pais_residencia: event.target.value,
      dial_code: country?.dialCode || prev.dial_code,
    }))
  }

  const handleSubmit = async (event) => {
    event.preventDefault()
    setError('')
    setFieldErrors({})
    if (!passwordValid || !passwordsMatch) return
    if (!captchaToken) {
      setError('Completá la verificación "No soy un robot"')
      return
    }

    setLoading(true)
    try {
      const res = await fetch(`${API_URL}/api/auth/register`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          nombre: form.nombre,
          apellido: form.apellido,
          email: form.email,
          pais_residencia: form.pais_residencia,
          telefono: `${form.dial_code} ${form.telefono.replace(/\D/g, '')}`,
          tipo_documento: form.tipo_documento,
          numero_dni: form.numero_dni,
          fecha_nacimiento: form.fecha_nacimiento,
          password: form.password,
          acepta_suscripcion: form.acepta_suscripcion,
          recaptcha_token: captchaToken,
        })
      })
      const data = await res.json()
      if (!res.ok) {
        if (data.field) setFieldErrors({ [data.field]: data.error })
        throw new Error(data.error || 'No se pudo completar el registro')
      }
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

  const fieldError = (field) =>
    fieldErrors[field] && <span className="field-error">{fieldErrors[field]}</span>

  return (
    <div className="login-container">
      <form className="login-card register-card" onSubmit={handleSubmit}>
        <h1>Crear cuenta</h1>

        <div className="form-grid">
          <div className="form-field">
            <label htmlFor="reg-nombre">Nombre</label>
            <input id="reg-nombre" autoComplete="given-name" value={form.nombre} onChange={update('nombre')} maxLength={100} required />
            {fieldError('nombre')}
          </div>
          <div className="form-field">
            <label htmlFor="reg-apellido">Apellido</label>
            <input id="reg-apellido" autoComplete="family-name" value={form.apellido} onChange={update('apellido')} maxLength={100} required />
            {fieldError('apellido')}
          </div>

          <div className="form-field full">
            <label htmlFor="reg-email">Correo electrónico</label>
            <input id="reg-email" type="email" autoComplete="email" value={form.email} onChange={update('email')} required />
            {fieldError('email')}
          </div>

          <div className="form-field">
            <label htmlFor="reg-pais">País de residencia</label>
            <select id="reg-pais" autoComplete="country-name" value={form.pais_residencia} onChange={handleCountryChange} required>
              {COUNTRIES.map((c) => <option key={c.name} value={c.name}>{c.name}</option>)}
            </select>
            {fieldError('pais_residencia')}
          </div>
          <div className="form-field">
            <label htmlFor="reg-telefono">Teléfono</label>
            <div className="phone-input">
              <select aria-label="Código de país" value={form.dial_code} onChange={update('dial_code')}>
                {DIAL_CODES.map((code) => <option key={code} value={code}>{code}</option>)}
              </select>
              <input
                id="reg-telefono"
                type="tel"
                inputMode="numeric"
                autoComplete="tel-national"
                placeholder="Cód. de área + número"
                pattern="[0-9 ]{6,20}"
                value={form.telefono}
                onChange={update('telefono')}
                required
              />
            </div>
            {fieldError('telefono')}
          </div>

          <div className="form-field">
            <label htmlFor="reg-tipo-doc">Tipo de documento</label>
            <select id="reg-tipo-doc" value={form.tipo_documento} onChange={update('tipo_documento')} required>
              {DOCUMENT_TYPES.map((d) => <option key={d.value} value={d.value}>{d.label}</option>)}
            </select>
            {fieldError('tipo_documento')}
          </div>
          <div className="form-field">
            <label htmlFor="reg-dni">Número de documento</label>
            <input
              id="reg-dni"
              inputMode={form.tipo_documento === 'DNI' ? 'numeric' : 'text'}
              value={form.numero_dni}
              onChange={update('numero_dni')}
              maxLength={20}
              required
            />
            {fieldError('numero_dni')}
          </div>

          <div className="form-field full">
            <label htmlFor="reg-nacimiento">Fecha de nacimiento</label>
            <input id="reg-nacimiento" type="date" autoComplete="bday" max={TODAY} value={form.fecha_nacimiento} onChange={update('fecha_nacimiento')} required />
            {fieldError('fecha_nacimiento')}
          </div>

          <div className="form-field full">
            <label htmlFor="reg-password">Contraseña</label>
            <PasswordInput
              id="reg-password"
              autoComplete="new-password"
              value={form.password}
              onChange={update('password')}
              aria-describedby="reg-password-rules"
              required
            />
            <PasswordRules id="reg-password-rules" password={form.password} />
            {fieldError('password')}
          </div>

          <div className="form-field full">
            <label htmlFor="reg-confirm">Confirmar contraseña</label>
            <PasswordInput
              id="reg-confirm"
              autoComplete="new-password"
              value={form.confirm_password}
              onChange={update('confirm_password')}
              aria-invalid={showMismatch}
              required
            />
            {showMismatch && <span className="field-error">Las contraseñas no coinciden</span>}
          </div>

          <label className="checkbox-field full">
            <input type="checkbox" checked={form.acepta_suscripcion} onChange={update('acepta_suscripcion')} />
            Quiero recibir novedades y promociones de Olaticket
          </label>
        </div>

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
        <button className="buy-btn" type="submit" disabled={loading || !passwordValid || !passwordsMatch}>
          {loading ? 'CREANDO CUENTA...' : 'Crear cuenta'}
        </button>

        <p className="auth-switch">
          ¿Ya tienes una cuenta?{' '}
          <button type="button" className="link-btn" onClick={onGoToLogin}>Ingresa aquí</button>
        </p>
      </form>
    </div>
  )
}

export default Register
