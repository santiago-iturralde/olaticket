import { useState } from 'react'
import { FaEye, FaEyeSlash } from 'react-icons/fa'

// Input de contraseña con el "ojito" para mostrar / ocultar el texto.
function PasswordInput({ id, value, onChange, autoComplete, ...props }) {
  const [visible, setVisible] = useState(false)

  return (
    <div className="password-field">
      <input
        id={id}
        type={visible ? 'text' : 'password'}
        autoComplete={autoComplete}
        value={value}
        onChange={onChange}
        {...props}
      />
      <button
        type="button"
        className="password-toggle"
        onClick={() => setVisible((v) => !v)}
        aria-label={visible ? 'Ocultar contraseña' : 'Mostrar contraseña'}
        aria-controls={id}
        aria-pressed={visible}
      >
        {visible ? <FaEyeSlash /> : <FaEye />}
      </button>
    </div>
  )
}

export default PasswordInput
