import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '../../lib/api'
import type { NotificationEngagementSetting } from '../../lib/api'
import { ToastProvider } from '../../components/synapse/Toast'
import { ExternalNotificationsCard } from './ExternalNotificationsCard'

vi.mock('../../lib/api', async (original) => ({
  ...(await original<typeof import('../../lib/api')>()),
  api: {
    me: vi.fn(),
    getNotificationEngagementSetting: vi.fn(),
    putNotificationEngagementSetting: vi.fn(),
  },
}))

function setting(over: Partial<NotificationEngagementSetting> = {}): NotificationEngagementSetting {
  return { engagement_id: 'eng-1', external_notifications: 'inherit', revision: 0, ...over }
}

function renderCard() {
  render(
    <ToastProvider>
      <ExternalNotificationsCard engagementId="eng-1" />
    </ToastProvider>,
  )
}

async function choose(label: string) {
  fireEvent.click(await screen.findByRole('combobox', { name: 'What leaves Synapse about this engagement' }))
  fireEvent.click(await screen.findByRole('option', { name: label }))
}

async function options(): Promise<string[]> {
  fireEvent.click(await screen.findByRole('combobox', { name: 'What leaves Synapse about this engagement' }))
  const listed = (await screen.findAllByRole('option')).map((o) => o.textContent ?? '')
  fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
  return listed
}

describe('ExternalNotificationsCard (#1360)', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() })
    vi.mocked(api.me).mockResolvedValue({ role: 'admin' } as never)
    vi.mocked(api.getNotificationEngagementSetting).mockResolvedValue(setting())
    vi.mocked(api.putNotificationEngagementSetting).mockImplementation(async (_id, value, revision) =>
      setting({ external_notifications: value, revision: revision + 1, updated_by: 'admin', updated_at: '2026-10-07T08:00:00Z' }),
    )
  })

  it('says there is no override while the engagement has none stored', async () => {
    renderCard()
    expect(await screen.findByText(/No override is set/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  })

  it('shows the loading state while the setting loads', async () => {
    vi.mocked(api.getNotificationEngagementSetting).mockReturnValue(new Promise(() => {}))
    renderCard()
    expect(await screen.findByText('Loading notification setting…')).toBeInTheDocument()
  })

  it('shows the permission state to a role without manage_integrations, without asking the server', async () => {
    vi.mocked(api.me).mockResolvedValue({ role: 'member' } as never)
    renderCard()
    expect(await screen.findByText(/Only tenant administrators and integration administrators/)).toBeInTheDocument()
    expect(api.getNotificationEngagementSetting).not.toHaveBeenCalled()
  })

  it('shows the permission state when the server answers 403', async () => {
    vi.mocked(api.getNotificationEngagementSetting).mockRejectedValue(new ApiError(403, 'forbidden'))
    renderCard()
    expect(await screen.findByText(/Only tenant administrators and integration administrators/)).toBeInTheDocument()
  })

  it('shows a load failure with a retry', async () => {
    vi.mocked(api.getNotificationEngagementSetting).mockRejectedValueOnce(new ApiError(500, 'database unavailable'))
    renderCard()
    expect(await screen.findByText('database unavailable')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText(/No override is set/)).toBeInTheDocument()
  })

  it('caps every channel at signal with the revision it read', async () => {
    renderCard()
    await choose('Signal only')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.putNotificationEngagementSetting).toHaveBeenCalledWith('eng-1', 'signal', 0))
    expect(await screen.findByText(/Last changed by admin/)).toBeInTheDocument()
  })

  it('asks before stopping every external notification', async () => {
    renderCard()
    await choose('Nothing leaves Synapse')
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('dialog', { name: 'Stop external notifications?' })).toBeInTheDocument()
    expect(api.putNotificationEngagementSetting).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Stop notifications' }))
    await waitFor(() => expect(api.putNotificationEngagementSetting).toHaveBeenCalledWith('eng-1', 'none', 0))
  })

  it('reloads after a concurrent change and says so', async () => {
    vi.mocked(api.putNotificationEngagementSetting).mockRejectedValueOnce(new ApiError(409, 'stale'))
    renderCard()
    await choose('Signal only')
    vi.mocked(api.getNotificationEngagementSetting).mockResolvedValue(setting({ external_notifications: 'signal', revision: 1, updated_by: 'other' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText(/Last changed by other/)).toBeInTheDocument()
    expect(api.getNotificationEngagementSetting).toHaveBeenCalledTimes(2)
  })

  it('offers an integration administrator only the stored value and lower ones', async () => {
    vi.mocked(api.me).mockResolvedValue({ role: 'integration_admin' } as never)
    vi.mocked(api.getNotificationEngagementSetting).mockResolvedValue(setting({ external_notifications: 'signal', revision: 1 }))
    renderCard()
    expect(await options()).toEqual(['Signal only', 'Nothing leaves Synapse'])
    expect(screen.getByText(/Only tenant administrators can let more out/)).toBeInTheDocument()
  })

  it('offers an administrator every value', async () => {
    vi.mocked(api.getNotificationEngagementSetting).mockResolvedValue(setting({ external_notifications: 'none', revision: 2 }))
    renderCard()
    expect(await options()).toEqual(['Each channel’s data class', 'Signal only', 'Nothing leaves Synapse'])
  })
})
