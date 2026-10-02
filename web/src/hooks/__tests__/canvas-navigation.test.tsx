import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { isInfiniteCanvasPreset } from '@/features/chat/lib/chat-links'

import { useSidebarData } from '../use-sidebar-data'

beforeEach(() => localStorage.clear())
afterEach(() => localStorage.clear())

function sidebarWith(chats: Array<Record<string, string>>) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], { chats })
  function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>
  }
  return renderHook(() => useSidebarData(), { wrapper: Wrapper }).result
}

describe('top-level canvas navigation', () => {
  it('an available canvas preset adds a direct entry beside Playground', () => {
    const result = sidebarWith([
      { 'Other chat': 'https://chat.example.com/' },
      {
        无限画布: 'https://lpss.online/canvas/canvas#baseUrl={address}&apiKey={key}',
      },
    ])
    const items =
      result.current.navGroups.find((group) => group.id === 'chat')?.items ?? []
    expect(items.map((item) => item.title)).toEqual([
      'Playground',
      'Infinite Canvas',
      'Chat',
    ])
    expect(items[1]).toMatchObject({ url: '/chat/1' })
    expect(items[1].items).toBeUndefined()
  })

  it('reordering presets updates the direct link instead of keeping a fixed index', () => {
    const result = sidebarWith([
      {
        'Renamed canvas':
          'https://lpss.online/canvas/canvas#baseUrl={address}&apiKey={key}',
      },
      { 'Other chat': 'https://chat.example.com/' },
    ])
    const entry = result.current.navGroups
      .flatMap((group) => group.items)
      .find((item) => item.title === 'Infinite Canvas')
    expect(entry?.url).toBe('/chat/0')
  })

  it('missing canvas presets do not expose a broken direct entry', () => {
    const result = sidebarWith([{ 'Other chat': 'https://chat.example.com/' }])
    expect(
      result.current.navGroups
        .flatMap((group) => group.items)
        .some((item) => item.title === 'Infinite Canvas')
    ).toBe(false)
  })

  it('malformed web presets do not crash sidebar rendering', () => {
    const result = sidebarWith([{ 'Broken chat': 'https://' }])
    expect(
      result.current.navGroups
        .flatMap((group) => group.items)
        .some((item) => item.title === 'Infinite Canvas')
    ).toBe(false)
  })
})

describe('canvas preset identification for sidebar promotion', () => {
  it('the configured canvas web route is recognized for removal from the nested list', () => {
    expect(
      isInfiniteCanvasPreset({
        id: '10',
        name: '无限画布',
        type: 'web',
        url: 'https://lpss.online/canvas/canvas#apiKey={key}',
      })
    ).toBe(true)
  })

  it('unrelated routes containing canvas only in the query stay in the nested list', () => {
    expect(
      isInfiniteCanvasPreset({
        id: '0',
        name: 'Other',
        type: 'web',
        url: 'https://chat.example.com/?next=/canvas/canvas',
      })
    ).toBe(false)
  })

  it('custom protocol presets are never promoted to an internal web route', () => {
    expect(
      isInfiniteCanvasPreset({
        id: '0',
        name: 'Client',
        type: 'custom-protocol',
        url: 'client://canvas/canvas',
      })
    ).toBe(false)
  })
})
