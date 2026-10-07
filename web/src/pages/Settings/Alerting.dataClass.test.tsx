import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../lib/api'
import type { NotificationChannel } from '../../lib/api'
import { resetCapabilityCache } from '../../lib/capabilities'
import type { Capability } from '../../lib/types'
import { Alerting } from './Alerting'
import { channelDataClass, defaultDataClass } from './ChannelDataClass'

vi.mock('../../lib/api', async (original) => ({
  ...(await original<typeof import('../../lib/api')>()),
  api: {
    me: vi.fn(),
    testAlert: vi.fn(),
    listCapabilities: vi.fn(),
    listNotificationChannels: vi.fn(),
    listNotificationRules: vi.fn(),
    listNotificationEventTypes: vi.fn(),
    notificationDeliveryPage: vi.fn(),
    listNotificationAttempts: vi.fn(),
    createNotificationChannel: vi.fn(),
    updateNotificationChannel: vi.fn(),
    listEngagements: vi.fn(),
    ownershipTeams: vi.fn(),
  },
}))

const capabilities: Capability[] = [
  {
    key: 'notifications', name: 'Tenant notifications', enabled: true,
    switch: 'SYNAPSE_NOTIFICATIONS_ENABLED', requires: [], values: [], planned: false,
  },
]

function channel(over: Partial<NotificationChannel>): NotificationChannel {
  return {
    id: 'c', name: 'Channel', type: 'slack', enabled: true, destination: 'https://hooks.slack.com/…',
    revision: 2, secret_version: 1, created_at: '', updated_at: '', ...over,
  }
}

function form(button: string) {
  const el = screen.getByRole('button', { name: button }).closest('form')
  if (!el) throw new Error('no channel form')
  return within(el)
}

async function options(name: string): Promise<string[]> {
  fireEvent.click(await screen.findByRole('combobox', { name }))
  const listed = (await screen.findAllByRole('option')).map((o) => o.textContent ?? '')
  fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
  return listed
}

describe('channel data class (#1360)', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    resetCapabilityCache()
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() })
    vi.mocked(api.me).mockResolvedValue({ role: 'admin' } as never)
    vi.mocked(api.listCapabilities).mockResolvedValue(capabilities)
    vi.mocked(api.listNotificationRules).mockResolvedValue([])
    vi.mocked(api.listNotificationEventTypes).mockResolvedValue([])
    vi.mocked(api.notificationDeliveryPage).mockResolvedValue({ items: [] })
    vi.mocked(api.listEngagements).mockResolvedValue([])
    vi.mocked(api.ownershipTeams).mockResolvedValue({ items: [] })
    vi.mocked(api.listNotificationChannels).mockResolvedValue([])
    vi.mocked(api.createNotificationChannel).mockResolvedValue(channel({}))
    vi.mocked(api.updateNotificationChannel).mockResolvedValue(channel({}))
  })

  it('mirrors the server defaults: chat at signal, email and webhooks at summary', () => {
    for (const type of ['slack', 'teams', 'telegram', 'google_chat', 'discord'] as const)
      expect(defaultDataClass(type)).toBe('signal')
    expect(defaultDataClass('email')).toBe('summary')
    expect(defaultDataClass('webhook')).toBe('summary')
    // A server that predates data classes sends none; the type's default stands in.
    expect(channelDataClass({ type: 'slack' })).toBe('signal')
    expect(channelDataClass({ type: 'slack', data_class: 'detail' })).toBe('detail')
  })

  it('starts a new channel at its type default and sends a class only when it was changed', async () => {
    render(<Alerting />)
    const add = await screen.findByRole('button', { name: 'Add channel' })
    const dataClass = await screen.findByRole('combobox', { name: 'Data class' })
    expect(dataClass).toHaveTextContent('Summary (default for this type)')
    // The class follows the type until the administrator picks one.
    for (const [type, want] of [['Slack incoming webhook', 'Signal'], ['Signed webhook', 'Summary']]) {
      fireEvent.click(screen.getByRole('combobox', { name: 'Type' }))
      fireEvent.click(await screen.findByRole('option', { name: type }))
      expect(dataClass).toHaveTextContent(want)
    }
    const f = form('Add channel')
    fireEvent.change(f.getByLabelText('Name'), { target: { value: 'Webhook' } })
    fireEvent.change(f.getByLabelText('Webhook URL'), { target: { value: 'https://hooks.example/in' } })
    fireEvent.change(f.getByLabelText('HMAC secret'), { target: { value: '0123456789abcdef' } })
    fireEvent.click(add)
    await waitFor(() => expect(api.createNotificationChannel).toHaveBeenCalled())
    expect(vi.mocked(api.createNotificationChannel).mock.calls[0][0].data_class).toBeUndefined()
  })

  it('lets an administrator raise a channel to detail', async () => {
    vi.mocked(api.listNotificationChannels).mockResolvedValue([channel({ name: 'SOC room', data_class: 'signal' })])
    render(<Alerting />)
    const item = within((await screen.findByText('SOC room')).closest('li') as HTMLElement)
    expect(item.getByText('Data class: Signal')).toBeInTheDocument()
    fireEvent.click(item.getByRole('button', { name: 'Edit channel' }))
    await screen.findByRole('button', { name: 'Save channel' })
    expect(await options('Data class')).toEqual(['Signal (default for this type)', 'Summary', 'Detail'])
    fireEvent.click(await screen.findByRole('combobox', { name: 'Data class' }))
    fireEvent.click(await screen.findByRole('option', { name: 'Detail' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save channel' }))
    await waitFor(() => expect(api.updateNotificationChannel).toHaveBeenCalled())
    expect(vi.mocked(api.updateNotificationChannel).mock.calls[0][1].data_class).toBe('detail')
  })

  it('offers an integration administrator only the stored class and lower ones', async () => {
    vi.mocked(api.me).mockResolvedValue({ role: 'integration_admin' } as never)
    vi.mocked(api.listNotificationChannels).mockResolvedValue([
      channel({ name: 'Mail', type: 'email', destination: 'ops@example.com', recipients: ['ops@example.com'], data_class: 'summary' }),
    ])
    render(<Alerting />)
    const item = within((await screen.findByText('Mail')).closest('li') as HTMLElement)
    fireEvent.click(item.getByRole('button', { name: 'Edit channel' }))
    await screen.findByRole('button', { name: 'Save channel' })
    expect(await options('Data class')).toEqual(['Signal', 'Summary (default for this type)'])
    expect(screen.getByText(/Only tenant administrators can raise it/)).toBeInTheDocument()
    fireEvent.click(await screen.findByRole('combobox', { name: 'Data class' }))
    fireEvent.click(await screen.findByRole('option', { name: 'Signal' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save channel' }))
    await waitFor(() => expect(api.updateNotificationChannel).toHaveBeenCalled())
    expect(vi.mocked(api.updateNotificationChannel).mock.calls[0][1].data_class).toBe('signal')
  })
})
