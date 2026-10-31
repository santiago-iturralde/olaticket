// Mismas reglas que valida el backend (validatePassword).
export const PASSWORD_RULES = [
  { label: 'Al menos 8 caracteres', test: (p) => p.length >= 8 },
  { label: 'Al menos una letra mayúscula', test: (p) => /\p{Lu}/u.test(p) },
  { label: 'Al menos un número', test: (p) => /\d/.test(p) },
]

export const isPasswordValid = (password) => PASSWORD_RULES.every((rule) => rule.test(password))
