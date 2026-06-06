import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App'

vi.mock('./api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./api')>()
  return { ...actual, calculate: vi.fn() }
})

import { ApiError, NetworkError, calculate } from './api'

const calculateMock = vi.mocked(calculate)

afterEach(() => {
  vi.clearAllMocks()
})

describe('<App />', () => {
  it('renders the form with operands a and b', () => {
    render(<App />)
    expect(
      screen.getByRole('heading', { name: /decimal-safe calculator/i }),
    ).toBeInTheDocument()
    expect(screen.getByLabelText('a')).toBeInTheDocument()
    expect(screen.getByLabelText('b')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /calculate/i })).toBeInTheDocument()
  })

  it('submits and shows the result string exactly as returned', async () => {
    calculateMock.mockResolvedValue({ result: '0.3', precision: 1 })
    const user = userEvent.setup()
    render(<App />)

    await user.type(screen.getByLabelText('a'), '0.1')
    await user.type(screen.getByLabelText('b'), '0.2')
    await user.click(screen.getByRole('button', { name: /calculate/i }))

    expect(await screen.findByText('0.3')).toBeInTheDocument()
    expect(calculateMock).toHaveBeenCalledWith({ operation: 'add', a: '0.1', b: '0.2' })
  })

  it('shows a friendly divide-by-zero message, never the raw code', async () => {
    calculateMock.mockRejectedValue(new ApiError('DIVIDE_BY_ZERO', 'division by zero', 400))
    const user = userEvent.setup()
    render(<App />)

    await user.selectOptions(screen.getByLabelText('Operation'), 'divide')
    await user.type(screen.getByLabelText('a'), '1')
    await user.type(screen.getByLabelText('b'), '0')
    await user.click(screen.getByRole('button', { name: /calculate/i }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Cannot divide by zero.')
    expect(alert).not.toHaveTextContent('DIVIDE_BY_ZERO')
  })

  it('blocks invalid input client-side without calling the API', async () => {
    const user = userEvent.setup()
    render(<App />)

    await user.type(screen.getByLabelText('a'), 'abc')
    await user.type(screen.getByLabelText('b'), '2')
    await user.click(screen.getByRole('button', { name: /calculate/i }))

    expect(await screen.findByText(/enter a valid number/i)).toBeInTheDocument()
    expect(calculateMock).not.toHaveBeenCalled()
  })

  it('shows a network error when the backend is unreachable', async () => {
    calculateMock.mockRejectedValue(
      new NetworkError('Could not reach the calculator service. Is it running?'),
    )
    const user = userEvent.setup()
    render(<App />)

    await user.type(screen.getByLabelText('a'), '1')
    await user.type(screen.getByLabelText('b'), '2')
    await user.click(screen.getByRole('button', { name: /calculate/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/could not reach/i)
  })

  it('hides operand b for the unary sqrt operation', async () => {
    const user = userEvent.setup()
    render(<App />)

    expect(screen.getByLabelText('b')).toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('Operation'), 'sqrt')
    expect(screen.getByLabelText('a')).toBeInTheDocument()
    expect(screen.queryByLabelText('b')).not.toBeInTheDocument()
  })
})
