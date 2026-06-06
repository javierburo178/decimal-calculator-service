// A labelled decimal input with an inline validation message. Presentation only.

interface OperandFieldProps {
  id: string
  label: string
  value: string
  error: string | null
  onChange: (value: string) => void
}

export function OperandField({ id, label, value, error, onChange }: OperandFieldProps) {
  const errorId = `${id}-error`
  return (
    <div className="field">
      <label className="field__label" htmlFor={id}>
        {label}
      </label>
      <input
        id={id}
        className="control"
        type="text"
        inputMode="decimal"
        autoComplete="off"
        spellCheck={false}
        placeholder="e.g. 0.1, -3, 1e10"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-invalid={error !== null}
        aria-describedby={error !== null ? errorId : undefined}
      />
      {error !== null && (
        <span id={errorId} className="field__error" role="alert">
          {error}
        </span>
      )}
    </div>
  )
}
