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
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { expect, it } from 'vitest'

import type { ReasoningEffort } from '../../../types'
import { ReasoningEffortControl } from '../reasoning-effort-control'

function Harness() {
  const [effort, setEffort] = useState<ReasoningEffort | null>(null)

  return (
    <>
      <ReasoningEffortControl
        disabled={false}
        onChange={setEffort}
        value={effort}
      />
      <output aria-label='Selected reasoning effort'>
        {effort ?? 'automatic'}
      </output>
    </>
  )
}

it('selects a heavy reasoning effort and resets back to automatic', async () => {
  const user = userEvent.setup()
  render(<Harness />)

  await user.click(
    screen.getByRole('button', { name: 'Adjust reasoning effort' })
  )
  expect(screen.getAllByText('Automatic')).toHaveLength(2)

  const slider = screen.getByLabelText('Reasoning effort')
  fireEvent.keyDown(slider, { key: 'End' })
  expect(screen.getByLabelText('Selected reasoning effort')).toHaveTextContent(
    'high'
  )

  await user.click(
    screen.getByRole('button', { name: 'Reset reasoning effort' })
  )
  expect(screen.getByLabelText('Selected reasoning effort')).toHaveTextContent(
    'automatic'
  )
})

it('selects the minimum supported reasoning effort', async () => {
  const user = userEvent.setup()
  render(<Harness />)

  await user.click(
    screen.getByRole('button', { name: 'Adjust reasoning effort' })
  )

  fireEvent.keyDown(screen.getByLabelText('Reasoning effort'), { key: 'Home' })
  expect(screen.getByLabelText('Selected reasoning effort')).toHaveTextContent(
    'minimal'
  )
})
