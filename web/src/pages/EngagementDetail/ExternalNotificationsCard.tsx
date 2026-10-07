import { useCallback, useEffect, useState } from 'react'
import { Save01 } from '@untitledui/icons'
import { Button, Card, ErrorState, Field, Select, Spinner } from '../../components/ui'
import { ConfirmDialog } from '../../components/synapse/ConfirmDialog'
import { useToast } from '../../components/synapse/Toast'
import { useFetch } from '../../hooks'
import { api, ApiError } from '../../lib/api'
import type { NotificationEngagementOverride, NotificationEngagementSetting } from '../../lib/api'
import { canManageIntegrations, isAdminRole } from '../../lib/roles'

/**
 * The engagement's external notification override (#1360), most to least permissive. The lower of
 * this and a channel's own data class is what a message about the engagement may carry.
 */
const OVERRIDES: { value: NotificationEngagementOverride; label: string; hint: string }[] = [
  {
    value: 'inherit',
    label: 'Each channel’s data class',
    hint: 'Messages about this engagement carry what each channel allows.',
  },
  {
    value: 'signal',
    label: 'Signal only',
    hint: 'Every channel is capped at signal: event type, severity, counts and a link, never names or titles.',
  },
  {
    value: 'none',
    label: 'Nothing leaves Synapse',
    hint: 'No channel message or personal email about this engagement is sent. In-app notices are kept.',
  },
]

function exposure(o: NotificationEngagementOverride): number {
  return OVERRIDES.length - 1 - OVERRIDES.findIndex((x) => x.value === o)
}

const PERMISSION_HINT =
  'Only tenant administrators and integration administrators can change what leaves Synapse about this engagement.'

/**
 * What leaves Synapse about one engagement. Lowering it needs manage_integrations; letting more
 * out needs administer, so without it the select offers only the stored value and lower ones. The
 * server enforces both and answers 403 on its own.
 */
export function ExternalNotificationsCard({ engagementId }: { engagementId: string }) {
  const { notify } = useToast()
  const { data: me } = useFetch(() => api.me(), { deps: [] })
  const canManage = canManageIntegrations(me?.role)
  const canRaise = isAdminRole(me?.role)
  const [setting, setSetting] = useState<NotificationEngagementSetting | null>(null)
  const [choice, setChoice] = useState<NotificationEngagementOverride>('inherit')
  const [loadError, setLoadError] = useState<string | null>(null)
  const [forbidden, setForbidden] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [confirmNone, setConfirmNone] = useState(false)

  const load = useCallback(async () => {
    setLoadError(null)
    setSetting(null)
    try {
      const s = await api.getNotificationEngagementSetting(engagementId)
      setSetting(s)
      setChoice(s.external_notifications)
    } catch (e) {
      if (e instanceof ApiError && e.status === 403) setForbidden(true)
      else setLoadError(e instanceof Error ? e.message : 'Could not load the notification setting')
    }
  }, [engagementId])

  useEffect(() => {
    if (canManage) void load()
  }, [canManage, load])

  async function save() {
    if (!setting) return
    setSaving(true)
    setSaveError(null)
    try {
      const stored = await api.putNotificationEngagementSetting(engagementId, choice, setting.revision)
      setSetting(stored)
      setChoice(stored.external_notifications)
      setConfirmNone(false)
      notify('Notification setting saved', 'success')
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        setConfirmNone(false)
        setSaveError('Someone else changed this setting. The current value has been reloaded; review it and save again.')
        void load()
      } else {
        setSaveError(e instanceof Error ? e.message : 'Could not save the notification setting')
      }
    } finally {
      setSaving(false)
    }
  }

  if (!me) return null
  if (!canManage || forbidden)
    return (
      <Card title="External notifications">
        <p className="text-sm text-tertiary">{PERMISSION_HINT}</p>
      </Card>
    )
  if (loadError)
    return (
      <Card title="External notifications">
        <ErrorState message={loadError} />
        <div className="mt-3">
          <Button variant="secondary" onClick={() => void load()}>
            Retry
          </Button>
        </div>
      </Card>
    )
  if (!setting)
    return (
      <Card title="External notifications">
        <Spinner label="Loading notification setting…" />
      </Card>
    )

  const ceiling = canRaise ? OVERRIDES.length - 1 : exposure(setting.external_notifications)
  const options = OVERRIDES.filter((o) => exposure(o.value) <= ceiling).map((o) => ({ value: o.value, label: o.label }))
  const selected = OVERRIDES.find((o) => o.value === choice)
  const changed = choice !== setting.external_notifications
  return (
    <Card title="External notifications">
      <div className="space-y-4">
        <p className="text-sm text-tertiary">
          {setting.revision === 0
            ? 'No override is set: each channel sends at its own data class.'
            : `Last changed${setting.updated_by ? ` by ${setting.updated_by}` : ''}${
                setting.updated_at ? ` on ${new Date(setting.updated_at).toLocaleString()}` : ''
              }.`}
        </p>
        <Field
          label="What leaves Synapse about this engagement"
          htmlFor="engagement-external-notifications"
          hint={
            (selected?.hint ?? '') +
            (!canRaise && setting.external_notifications !== 'inherit'
              ? ' Only tenant administrators can let more out.'
              : '')
          }
        >
          <Select
            id="engagement-external-notifications"
            className="w-full sm:w-96"
            disabled={saving}
            value={choice}
            onValueChange={(v) => setChoice(v as NotificationEngagementOverride)}
            options={options}
          />
        </Field>
        <div className="flex flex-wrap items-center gap-3">
          <Button
            disabled={!changed}
            loading={saving && !confirmNone}
            onClick={() => (choice === 'none' ? setConfirmNone(true) : void save())}
          >
            <Save01 className="size-4" />
            Save
          </Button>
          {saveError && !confirmNone && <ErrorState message={saveError} />}
        </div>
      </div>
      <ConfirmDialog
        open={confirmNone}
        title="Stop external notifications?"
        description="Nothing about this engagement will be sent to channels or by personal email. Messages already queued are cancelled and are not sent later, even if you change this back."
        confirmLabel="Stop notifications"
        busy={saving}
        error={saveError}
        onConfirm={() => void save()}
        onCancel={() => {
          setConfirmNone(false)
          setSaveError(null)
        }}
      />
    </Card>
  )
}
