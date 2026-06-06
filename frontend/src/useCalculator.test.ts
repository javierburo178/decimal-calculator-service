import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useCalculator } from './useCalculator'

// Mock only the transport; keep ApiError/NetworkError/isUnary real.
vi.mock('./api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./api')>()
  return { ...actual, calculate: vi.fn() }
})

import { ApiError, NetworkError, calculate } from './api'

const calculateMock = vi.mocked(calculate)

afterEach(() => {
  vi.clearAllMocks()
})

describe('useCalculator', () => {
  it('runs a valid calculation and exposes the result', async () => {
    calculateMock.mockResolvedValue({ result: '0.3', precision: 1 })
    const { result } = renderHook(() => useCalculator())

    act(() => result.current.setA('0.1'))
    act(() => result.current.setB('0.2'))
    await act(async () => {
      await result.current.submit()
    })

    expect(calculateMock).toHaveBeenCalledWith({ operation: 'add', a: '0.1', b: '0.2' })
    expect(result.current.result).toEqual({ result: '0.3', precision: 1 })
    expect(result.current.error).toBeNull()
  })

  it('rejects empty/invalid input client-side without calling the API', async () => {
    const { result } = renderHook(() => useCalculator())

    // a left empty, b non-numeric
    act(() => result.current.setB('abc'))
    await act(async () => {
      await result.current.submit()
    })

    expect(calculateMock).not.toHaveBeenCalled()
    expect(result.current.fieldErrors.a).toBeTruthy()
    expect(result.current.fieldErrors.b).toBeTruthy()
    expect(result.current.result).toBeNull()
  })

  it('maps a backend error code to a human message, never the raw code', async () => {
    calculateMock.mockRejectedValue(new ApiError('DIVIDE_BY_ZERO', 'division by zero', 400))
    const { result } = renderHook(() => useCalculator())

    act(() => result.current.setOperation('divide'))
    act(() => result.current.setA('1'))
    act(() => result.current.setB('0'))
    await act(async () => {
      await result.current.submit()
    })

    expect(result.current.error).toBe('Cannot divide by zero.')
    expect(result.current.error).not.toContain('DIVIDE_BY_ZERO')
    expect(result.current.result).toBeNull()
  })

  it('surfaces a network error message', async () => {
    calculateMock.mockRejectedValue(
      new NetworkError('Could not reach the calculator service. Is it running?'),
    )
    const { result } = renderHook(() => useCalculator())

    act(() => result.current.setA('1'))
    act(() => result.current.setB('2'))
    await act(async () => {
      await result.current.submit()
    })

    expect(result.current.error).toMatch(/could not reach/i)
  })

  it('treats sqrt as unary: b not required, null sent', async () => {
    calculateMock.mockResolvedValue({ result: '2', precision: 1 })
    const { result } = renderHook(() => useCalculator())

    act(() => result.current.setOperation('sqrt'))
    expect(result.current.bRequired).toBe(false)

    act(() => result.current.setA('4'))
    await act(async () => {
      await result.current.submit()
    })

    expect(calculateMock).toHaveBeenCalledWith({ operation: 'sqrt', a: '4', b: null })
    expect(result.current.result).toEqual({ result: '2', precision: 1 })
  })
})
