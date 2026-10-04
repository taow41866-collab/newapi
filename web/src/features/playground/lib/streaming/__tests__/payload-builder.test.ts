/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { describe, expect, it } from 'vitest'

import type { ParameterEnabled, PlaygroundConfig } from '../../../types'
import { buildChatCompletionPayload } from '../payload-builder'

const baseConfig: PlaygroundConfig = {
  model: 'gpt-6.1-sol',
  group: 'default',
  temperature: 0.7,
  top_p: 1,
  max_tokens: 4096,
  frequency_penalty: 0,
  presence_penalty: 0,
  seed: null,
  reasoning_effort: null,
  stream: true,
}

const parameterEnabled: ParameterEnabled = {
  temperature: true,
  top_p: true,
  max_tokens: false,
  frequency_penalty: true,
  presence_penalty: true,
  seed: false,
}

describe('playground reasoning effort payload', () => {
  it('sends a selected reasoning effort to the chat endpoint', () => {
    const payload = buildChatCompletionPayload(
      [],
      {
        ...baseConfig,
        reasoning_effort: 'high',
      },
      parameterEnabled
    )

    expect(payload.reasoning_effort).toBe('high')
  })

  it('preserves the extra-high reasoning effort supported by the relay', () => {
    const payload = buildChatCompletionPayload(
      [],
      {
        ...baseConfig,
        reasoning_effort: 'xhigh',
      },
      parameterEnabled
    )

    expect(payload.reasoning_effort).toBe('xhigh')
  })

  it('omits reasoning effort when automatic mode is selected', () => {
    const payload = buildChatCompletionPayload(
      [],
      {
        ...baseConfig,
        reasoning_effort: null,
      },
      parameterEnabled
    )

    expect('reasoning_effort' in payload).toBe(false)
  })
})
