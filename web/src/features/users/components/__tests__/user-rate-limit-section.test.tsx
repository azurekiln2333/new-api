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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { UserRateLimitSection } from '../user-rate-limit-section'

const apiMocks = vi.hoisted(() => ({
  getUserRateLimit: vi.fn(),
  updateUserRateLimit: vi.fn(),
  deleteUserRateLimit: vi.fn(),
}))

vi.mock('../../api', () => ({
  getUserRateLimit: apiMocks.getUserRateLimit,
  updateUserRateLimit: apiMocks.updateUserRateLimit,
  deleteUserRateLimit: apiMocks.deleteUserRateLimit,
}))

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

function groupFollowedState() {
  return {
    success: true,
    data: {
      user_id: 7,
      group: 'vip',
      enabled: false,
      total_count: 0,
      success_count: 0,
      group_configured: true,
      group_total_count: 200,
      group_success_count: 100,
    },
  }
}

function customLimitState(totalCount: number, successCount: number) {
  const state = groupFollowedState()
  state.data.enabled = true
  state.data.total_count = totalCount
  state.data.success_count = successCount
  return state
}

describe('user rate limit section', () => {
  beforeEach(() => {
    apiMocks.getUserRateLimit.mockResolvedValue(groupFollowedState())
    apiMocks.updateUserRateLimit.mockResolvedValue(customLimitState(20, 5))
    apiMocks.deleteUserRateLimit.mockResolvedValue(groupFollowedState())
  })

  it('shows the group limit while the user follows the group', async () => {
    render(<UserRateLimitSection userId={7} />)

    expect(
      await screen.findByText(
        'Group limit: 200 requests (100 successful) per period'
      )
    ).toBeInTheDocument()
    expect(screen.getByRole('switch')).not.toBeChecked()
  })

  it('saves a custom limit for the user', async () => {
    render(<UserRateLimitSection userId={7} />)
    const user = userEvent.setup()

    await user.click(await screen.findByRole('switch'))
    const [maxRequests, maxSuccess] = screen.getAllByRole(
      'spinbutton'
    ) as HTMLInputElement[]
    fireEvent.change(maxRequests, { target: { value: '20' } })
    fireEvent.change(maxSuccess, { target: { value: '5' } })
    await user.click(screen.getByRole('button', { name: 'Save rate limit' }))

    await waitFor(() =>
      expect(apiMocks.updateUserRateLimit).toHaveBeenCalledWith(7, {
        total_count: 20,
        success_count: 5,
      })
    )
    expect(apiMocks.deleteUserRateLimit).not.toHaveBeenCalled()
  })

  it('removes the custom limit so the user follows the group again', async () => {
    apiMocks.getUserRateLimit.mockResolvedValue(customLimitState(20, 5))
    render(<UserRateLimitSection userId={7} />)
    const user = userEvent.setup()

    const toggle = await screen.findByRole('switch')
    await waitFor(() => expect(toggle).toBeChecked())
    await user.click(toggle)
    await user.click(screen.getByRole('button', { name: 'Save rate limit' }))

    await waitFor(() =>
      expect(apiMocks.deleteUserRateLimit).toHaveBeenCalledWith(7)
    )
    expect(apiMocks.updateUserRateLimit).not.toHaveBeenCalled()
  })

  it('rejects a negative total before calling the server', async () => {
    render(<UserRateLimitSection userId={7} />)
    const user = userEvent.setup()

    await user.click(await screen.findByRole('switch'))
    const [maxRequests] = screen.getAllByRole('spinbutton')
    fireEvent.change(maxRequests, { target: { value: '-1' } })
    await user.click(screen.getByRole('button', { name: 'Save rate limit' }))

    expect(
      await screen.findByText(
        'Total requests must be 0 or more, and successful requests must be 1 or more'
      )
    ).toBeInTheDocument()
    expect(apiMocks.updateUserRateLimit).not.toHaveBeenCalled()
  })
})
