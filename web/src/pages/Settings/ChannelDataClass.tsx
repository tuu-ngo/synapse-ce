import type { NotificationChannelType, NotificationDataClass } from '../../lib/api'
import { Field, Pill, Select } from '../../components/ui'
import { CHANNEL_FAMILY } from './ChannelTemplateBinding'

/**
 * The data classes of EPIC D7 (#1360), least to most sensitive. Each one names the most sensitive
 * content a channel's messages may carry; the server drops every variable above it.
 */
export const DATA_CLASSES: { value: NotificationDataClass; label: string; hint: string }[] = [
  {
    value: 'signal',
    label: 'Signal',
    hint: 'Event type, severity, counts and a link back to Synapse. No names, titles or hosts.',
  },
  {
    value: 'summary',
    label: 'Summary',
    hint: 'Adds engagement, project and finding names, titles and target hosts.',
  },
  {
    value: 'detail',
    label: 'Detail',
    hint: 'Adds advisories, assets, file paths and item lists.',
  },
]

function rank(c: NotificationDataClass): number {
  return DATA_CLASSES.findIndex((d) => d.value === c)
}

/**
 * The class a channel of this type starts at. It mirrors notification.DefaultDataClass on the
 * server: chat channels land in shared rooms and on phones, so they start at signal.
 */
export function defaultDataClass(type: NotificationChannelType): NotificationDataClass {
  return CHANNEL_FAMILY[type] === 'chat' ? 'signal' : 'summary'
}

/** A channel's class, reading a server that predates data classes as the type's default. */
export function channelDataClass(channel: {
  type: NotificationChannelType
  data_class?: NotificationDataClass
}): NotificationDataClass {
  return channel.data_class ?? defaultDataClass(channel.type)
}

/**
 * The data class select of the channel form. Raising a class lets more leave Synapse, so the
 * server keeps it to administer; without it the select offers only the stored class and lower
 * ones. The server refuses a raise with 403 regardless.
 */
export function ChannelDataClassField({
  type,
  value,
  stored,
  canRaise,
  onChange,
  disabled,
}: {
  type: NotificationChannelType
  value: NotificationDataClass
  /** The class the channel has now; absent while creating one. */
  stored?: NotificationDataClass
  canRaise: boolean
  onChange: (value: NotificationDataClass) => void
  disabled?: boolean
}) {
  const ceiling = canRaise || !stored ? DATA_CLASSES.length - 1 : rank(stored)
  const options = DATA_CLASSES.filter((d) => rank(d.value) <= ceiling).map((d) => ({
    value: d.value,
    label: d.value === defaultDataClass(type) ? `${d.label} (default for this type)` : d.label,
  }))
  const selected = DATA_CLASSES.find((d) => d.value === value)
  return (
    <Field
      label="Data class"
      htmlFor="notification-data-class"
      hint={
        (selected?.hint ?? '') +
        (!canRaise && stored && stored !== 'detail' ? ' Only tenant administrators can raise it.' : '')
      }
    >
      <Select
        id="notification-data-class"
        disabled={disabled}
        value={value}
        onValueChange={(v) => onChange(v as NotificationDataClass)}
        options={options}
      />
    </Field>
  )
}

/** The class a channel sends at, as a pill in the channel list. */
export function DataClassPill({ value }: { value: NotificationDataClass }) {
  const label = DATA_CLASSES.find((d) => d.value === value)?.label ?? value
  return <Pill className="text-tertiary">Data class: {label}</Pill>
}
