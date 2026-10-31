import { useEffect, useRef } from 'react'
import { FaCheckCircle, FaTimesCircle } from 'react-icons/fa'

// Cartel a pantalla completa: verde (adelante) o rojo (rechazada / ya usada). El foco va al
// botón para seguir con Enter o con el lector USB.
function ScanResult({ result, buttonLabel, onNext, footer }) {
  const buttonRef = useRef(null)
  useEffect(() => {
    buttonRef.current?.focus()
  }, [result])

  return (
    <div
      className={`scan-result ${result.ok ? 'scan-ok' : 'scan-denied'}`}
      role="alertdialog"
      aria-modal="true"
      aria-labelledby="scan-result-title"
      aria-describedby="scan-result-detail"
    >
      {result.ok ? <FaCheckCircle aria-hidden="true" /> : <FaTimesCircle aria-hidden="true" />}
      <h2 id="scan-result-title">{result.title}</h2>
      <p id="scan-result-detail">{result.detail}</p>
      {result.ticket && (
        <dl className="scan-ticket">
          <div><dt>Titular</dt><dd>{result.ticket.nombre}</dd></div>
          <div><dt>DNI</dt><dd>{result.ticket.dni}</dd></div>
          {result.ticket.sector && <div><dt>Sector</dt><dd>{result.ticket.sector}</dd></div>}
          <div><dt>Entrada</dt><dd>#{result.ticket.id}</dd></div>
        </dl>
      )}
      <button type="button" ref={buttonRef} className="scan-next" onClick={onNext}>
        {buttonLabel}
      </button>
      {footer}
    </div>
  )
}

export default ScanResult
