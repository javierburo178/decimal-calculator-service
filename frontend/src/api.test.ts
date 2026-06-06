import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, NetworkError, calculate } from './api'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('calculate (transport)', () => {
  it('sends operands a/b as JSON strings', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ result: '0.3', precision: 1 }))
    vi.stubGlobal('fetch', fetchMock)

    await calculate({ operation: 'add', a: '0.1', b: '0.2' })

    const init = fetchMock.mock.calls[0][1]
    const body = JSON.parse(init.body as string)
    expect(body).toEqual({ operation: 'add', a: '0.1', b: '0.2' })
    // The whole point: operands leave the browser as strings, not numbers.
    expect(typeof body.a).toBe('string')
    expect(typeof body.b).toBe('string')
  })

  it('returns the result as a string, never coerced to a number', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({ result: '0.3333333333333333333333333333', precision: 28 }),
      ),
    )

    const res = await calculate({ operation: 'divide', a: '1', b: '3' })

    expect(res.result).toBe('0.3333333333333333333333333333')
    expect(typeof res.result).toBe('string')
  })

  it('sends null for the second operand on a unary op', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ result: '2', precision: 1 }))
    vi.stubGlobal('fetch', fetchMock)

    await calculate({ operation: 'sqrt', a: '4', b: null })

    const body = JSON.parse(fetchMock.mock.calls[0][1].body as string)
    expect(body).toEqual({ operation: 'sqrt', a: '4', b: null })
  })

  it('throws ApiError carrying the backend code on a 4xx', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({ error: 'division by zero', code: 'DIVIDE_BY_ZERO' }, 400),
      ),
    )

    const err = await calculate({
      operation: 'divide',
      a: '1',
      b: '0',
    }).catch((e: unknown) => e)

    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).code).toBe('DIVIDE_BY_ZERO')
    expect((err as ApiError).status).toBe(400)
  })

  it('throws NetworkError when the request itself fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('failed to fetch')))

    await expect(
      calculate({ operation: 'add', a: '1', b: '2' }),
    ).rejects.toBeInstanceOf(NetworkError)
  })
})
