import { FaCheckCircle, FaRegCircle } from 'react-icons/fa'
import { PASSWORD_RULES } from './passwordPolicy'

// Checklist en tiempo real debajo del campo "Contraseña".
function PasswordRules({ id, password }) {
  return (
    <ul id={id} className="password-rules">
      {PASSWORD_RULES.map((rule) => {
        const ok = rule.test(password)
        return (
          <li key={rule.label} className={ok ? 'ok' : ''}>
            {ok ? <FaCheckCircle aria-hidden="true" /> : <FaRegCircle aria-hidden="true" />}
            <span>{rule.label}</span>
            <span className="sr-only">{ok ? ' (cumplido)' : ' (pendiente)'}</span>
          </li>
        )
      })}
    </ul>
  )
}

export default PasswordRules
