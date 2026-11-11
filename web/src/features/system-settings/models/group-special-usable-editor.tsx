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
import { useMutation } from '@tanstack/react-query'
import {
  AlertTriangle,
  ChevronDown,
  ChevronUp,
  Plus,
  Trash2,
} from 'lucide-react'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import { useSystemOptions } from '../hooks/use-system-options'
import { useUpdateOption } from '../hooks/use-update-option'

const sectionCardClassName = 'min-w-0 shadow-none'
const sectionHeaderClassName = 'gap-2 border-b'

export function GroupLevelsEditor(props: {
  groupOptions: string[]
  userUsableGroups: string
  specialGroups: string
}) {
  const { t } = useTranslation()
  const query = useSystemOptions()
  const save = useUpdateOption()
  const [draft, setDraft] = useState<Record<string, string> | null>(null)
  const [previewGroup, setPreviewGroup] = useState<string | null>(null)
  const preview = useMutation({
    mutationFn: async (request: {
      user_group: string
      levels: Record<string, number>
      usable_groups: unknown
      special_groups: unknown
    }) => {
      const response = await api.post<{ success: boolean; data: string[] }>(
        '/api/group/preview',
        request
      )
      return requireServerSuccess(response.data).data
    },
  })
  if (query.isPending) return <LoadingState />
  if (query.isError) {
    return (
      <ErrorState
        title={t('Failed to load settings')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  let saved: Record<string, number>
  try {
    saved = JSON.parse(
      query.data.data?.find((option) => option.key === 'GroupLevels')?.value ||
        '{}'
    )
    if (!saved || typeof saved !== 'object' || Array.isArray(saved)) {
      throw new Error('Invalid group levels')
    }
  } catch {
    return (
      <ErrorState
        title={t('Failed to load settings')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  const values =
    draft ??
    Object.fromEntries(
      Object.entries(saved).map(([name, value]) => [name, String(value)])
    )
  const groups = [
    ...new Set([...props.groupOptions, ...Object.keys(values)]),
  ].filter((group) => group !== 'auto')
  const invalid = Object.values(values).some(
    (value) =>
      value !== '' &&
      (!/^-?\d+$/.test(value) ||
        Number(value) < -2147483648 ||
        Number(value) > 2147483647)
  )
  let manual: unknown
  let special: unknown
  let invalidVisibility = false
  try {
    manual = JSON.parse(props.userUsableGroups || '{}')
    special = JSON.parse(props.specialGroups || '{}')
  } catch {
    invalidVisibility = true
  }
  const levels = Object.fromEntries(
    Object.entries(values)
      .filter(([, value]) => value !== '')
      .map(([name, value]) => [name, Number(value)])
  )
  const dirty =
    Object.keys(saved).length !== Object.keys(levels).length ||
    Object.entries(levels).some(([name, value]) => saved[name] !== value)
  const previewRequest = {
    user_group: previewGroup ?? '',
    levels,
    usable_groups: manual,
    special_groups: special,
  }
  const previewCurrent =
    !invalid &&
    !invalidVisibility &&
    JSON.stringify(preview.variables) === JSON.stringify(previewRequest)
  return (
    <Card className={sectionCardClassName}>
      <CardHeader className={sectionHeaderClassName}>
        <CardTitle>{t('Group levels')}</CardTitle>
        <CardDescription>
          {t(
            'Higher levels include all lower levels. Leave blank to keep manual visibility only. Manual grants are always included.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-4'>
        {groups.map((group) => (
          <div key={group} className='grid min-w-0 gap-2 sm:grid-cols-2'>
            <Label htmlFor={`group-level-${group}`} className='break-all'>
              {group}
            </Label>
            <Input
              id={`group-level-${group}`}
              aria-label={t('Level for {{group}}', { group })}
              type='number'
              step={1}
              min={-2147483648}
              max={2147483647}
              value={values[group] ?? ''}
              disabled={save.isPending}
              onChange={(event) => {
                setDraft({ ...values, [group]: event.target.value })
                preview.reset()
              }}
            />
          </div>
        ))}
        {invalid && (
          <p role='alert' className='text-destructive text-sm'>
            {t('Group levels must map group names to integers')}
          </p>
        )}
        <Button
          disabled={invalid || save.isPending || !dirty}
          onClick={() =>
            save.mutate({ key: 'GroupLevels', value: JSON.stringify(levels) })
          }
        >
          {t('Save group levels')}
        </Button>
        <div className='flex flex-wrap items-center gap-2'>
          <Combobox
            aria-label={t('Select user group to preview')}
            options={groups.map((group) => ({ value: group, label: group }))}
            value={previewGroup}
            onValueChange={(value) => {
              setPreviewGroup(value)
              preview.reset()
            }}
            placeholder={t('Select user group to preview')}
          />
          <Button
            variant='outline'
            disabled={
              invalid || invalidVisibility || !previewGroup || preview.isPending
            }
            onClick={() => {
              if (!previewGroup) return
              preview.mutate(previewRequest)
            }}
          >
            {t('Preview visible groups')}
          </Button>
        </div>
        {previewCurrent && preview.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {t('Failed to load groups')}
          </p>
        )}
        {previewCurrent && preview.isSuccess && (
          <p role='status' className='text-sm break-words'>
            {preview.data.join(', ') || t('No groups available')}
          </p>
        )}
      </CardContent>
    </Card>
  )
}

type Rule = {
  _id: string
  userGroup: string
  visible: boolean
  targetGroup: string
  description: string
}

let _idCounter = 0
function uid() {
  return `gsu_${++_idCounter}`
}

// Raw keys use +: (add), -: (remove), or no prefix (also add).
// The UI collapses this to visible/hidden and serializes visible rules
// with the +: prefix, which the backend treats identically to no prefix.
function parseRawKey(rawKey: string): { visible: boolean; groupName: string } {
  if (rawKey.startsWith('-:')) {
    return { visible: false, groupName: rawKey.slice(2) }
  }
  if (rawKey.startsWith('+:')) {
    return { visible: true, groupName: rawKey.slice(2) }
  }
  return { visible: true, groupName: rawKey }
}

function toRawKey(visible: boolean, groupName: string): string {
  return visible ? `+:${groupName}` : `-:${groupName}`
}

function safeParseJson(str: string): Record<string, Record<string, string>> {
  if (!str || !str.trim()) return {}
  try {
    return JSON.parse(str) as Record<string, Record<string, string>>
  } catch {
    return {}
  }
}

function flattenRules(nested: Record<string, Record<string, string>>): Rule[] {
  const rules: Rule[] = []
  for (const [userGroup, inner] of Object.entries(nested)) {
    if (typeof inner !== 'object' || inner === null) continue
    for (const [rawKey, desc] of Object.entries(inner)) {
      const { visible, groupName } = parseRawKey(rawKey)
      let description = ''
      if (!visible) {
        description = 'remove'
      } else if (typeof desc === 'string') {
        description = desc
      }
      rules.push({
        _id: uid(),
        userGroup,
        visible,
        targetGroup: groupName,
        description,
      })
    }
  }
  return rules
}

function serializeRules(rules: Rule[]): string {
  const result: Record<string, Record<string, string>> = {}
  for (const { userGroup, visible, targetGroup, description } of rules) {
    if (!userGroup || !targetGroup) continue
    if (!result[userGroup]) result[userGroup] = {}
    result[userGroup][toRawKey(visible, targetGroup)] = description
  }
  return Object.keys(result).length === 0
    ? '{}'
    : JSON.stringify(result, null, 2)
}

type GroupSelectProps = {
  options: string[]
  value: string
  placeholder: string
  onValueChange: (value: string) => void
  className?: string
}

function GroupSelect(props: GroupSelectProps) {
  const knownOptions = useMemo(() => {
    if (props.value && !props.options.includes(props.value)) {
      return [props.value, ...props.options]
    }
    return props.options
  }, [props.options, props.value])

  return (
    <Combobox
      options={knownOptions.map((name) => ({ value: name, label: name }))}
      value={props.value}
      onValueChange={(value) => {
        if (value) props.onValueChange(value)
      }}
      className={props.className}
      placeholder={props.placeholder}
      aria-label={props.placeholder}
    />
  )
}

type GroupSpecialUsableRulesEditorProps = {
  value: string
  groupOptions: string[]
  onChange: (value: string) => void
}

type GroupSectionProps = {
  groupName: string
  items: Rule[]
  groupOptions: string[]
  onUpdate: (id: string, field: keyof Rule, val: string | boolean) => void
  onRemove: (id: string) => void
  onAdd: (groupName: string) => void
  onRemoveGroup: (groupName: string) => void
}

function GroupSection(props: GroupSectionProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(true)
  const isKnownGroup = props.groupOptions.includes(props.groupName)

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <div className='rounded-lg border'>
        <div className='flex items-center justify-between gap-2 p-3'>
          <CollapsibleTrigger
            render={
              <Button
                variant='ghost'
                className='h-auto min-w-0 justify-start whitespace-normal'
              />
            }
            aria-label={t('Rules for {{group}}', { group: props.groupName })}
          >
            {open ? (
              <ChevronUp className='h-4 w-4' />
            ) : (
              <ChevronDown className='h-4 w-4' />
            )}
            <span className='min-w-0 truncate font-semibold'>
              {props.groupName}
            </span>
            {!isKnownGroup && (
              <StatusBadge variant='danger' copyable={false}>
                <AlertTriangle className='mr-1 h-3 w-3' />
                {t('Not in pricing table')}
              </StatusBadge>
            )}
            <StatusBadge variant='neutral' copyable={false}>
              {props.items.length} {t('rules')}
            </StatusBadge>
          </CollapsibleTrigger>
          <div className='flex shrink-0 items-center gap-1'>
            <Button
              variant='ghost'
              size='sm'
              className='h-7 w-7 p-0'
              aria-label={t('Add rule')}
              onClick={() => {
                props.onAdd(props.groupName)
                setOpen(true)
              }}
            >
              <Plus className='h-4 w-4' />
            </Button>
            <Button
              variant='ghost'
              size='sm'
              className='text-destructive h-7 w-7 p-0'
              aria-label={t('Remove {{group}}', { group: props.groupName })}
              onClick={() => props.onRemoveGroup(props.groupName)}
            >
              <Trash2 className='h-4 w-4' />
            </Button>
          </div>
        </div>
        <CollapsibleContent>
          <div className='space-y-2 border-t p-3'>
            {props.items.map((rule) => (
              <div
                key={rule._id}
                className='grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded-lg border p-3 sm:grid-cols-[130px_minmax(0,1fr)_minmax(0,1fr)_auto] sm:border-0 sm:p-0'
              >
                <Select
                  value={rule.visible ? 'visible' : 'hidden'}
                  onValueChange={(v) =>
                    v !== null &&
                    props.onUpdate(rule._id, 'visible', v === 'visible')
                  }
                >
                  <SelectTrigger
                    className='w-full sm:w-[130px]'
                    aria-label={t('Group visibility')}
                  >
                    <SelectValue>
                      <StatusBadge
                        label={rule.visible ? t('Extra visible') : t('Hidden')}
                        variant={rule.visible ? 'info' : 'danger'}
                        copyable={false}
                      />
                    </SelectValue>
                  </SelectTrigger>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='visible'>
                        <StatusBadge
                          label={t('Extra visible')}
                          variant='info'
                          copyable={false}
                        />
                      </SelectItem>
                      <SelectItem value='hidden'>
                        <StatusBadge
                          label={t('Hidden')}
                          variant='danger'
                          copyable={false}
                        />
                      </SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <div className='col-start-1 row-start-2 flex min-w-0 items-center gap-1.5 sm:col-start-auto sm:row-start-auto'>
                  <GroupSelect
                    className='min-w-0 flex-1'
                    options={props.groupOptions}
                    value={rule.targetGroup}
                    placeholder={t('Group name')}
                    onValueChange={(v) =>
                      props.onUpdate(rule._id, 'targetGroup', v)
                    }
                  />
                  {rule.targetGroup &&
                    !props.groupOptions.includes(rule.targetGroup) && (
                      <AlertTriangle
                        className='text-destructive h-4 w-4 shrink-0'
                        aria-label={t('Not in pricing table')}
                      />
                    )}
                </div>
                {rule.visible ? (
                  <Input
                    className='col-start-1 row-start-3 min-w-0 sm:col-start-auto sm:row-start-auto'
                    aria-label={t('Description')}
                    value={rule.description}
                    placeholder={t('Description')}
                    onChange={(e) =>
                      props.onUpdate(rule._id, 'description', e.target.value)
                    }
                  />
                ) : (
                  <div className='text-muted-foreground hidden px-3 text-sm sm:block'>
                    -
                  </div>
                )}
                <Button
                  variant='ghost'
                  size='sm'
                  className='text-destructive col-start-2 row-start-1 size-8 p-0 sm:col-start-auto sm:row-start-auto'
                  aria-label={t('Delete rule')}
                  onClick={() => props.onRemove(rule._id)}
                >
                  <Trash2 className='h-4 w-4' />
                </Button>
              </div>
            ))}
          </div>
        </CollapsibleContent>
      </div>
    </Collapsible>
  )
}

export function GroupSpecialUsableRulesEditor(
  props: GroupSpecialUsableRulesEditorProps
) {
  const { t } = useTranslation()
  const [rules, setRules] = useState<Rule[]>(() =>
    flattenRules(safeParseJson(props.value))
  )

  const { onChange } = props
  const emitChange = useCallback(
    (newRules: Rule[]) => {
      setRules(newRules)
      onChange(serializeRules(newRules))
    },
    [onChange]
  )

  const updateRule = useCallback(
    (id: string, field: keyof Rule, val: string | boolean) => {
      emitChange(
        rules.map((r) => {
          if (r._id !== id) return r
          const updated = { ...r, [field]: val }
          if (field === 'visible' && val === false) {
            updated.description = 'remove'
          } else if (field === 'visible' && val === true && !r.visible) {
            if (updated.description === 'remove') updated.description = ''
          }
          return updated
        })
      )
    },
    [rules, emitChange]
  )

  const removeRule = useCallback(
    (id: string) => emitChange(rules.filter((r) => r._id !== id)),
    [rules, emitChange]
  )

  const removeGroup = useCallback(
    (groupName: string) =>
      emitChange(rules.filter((r) => r.userGroup !== groupName)),
    [rules, emitChange]
  )

  const addRuleToGroup = useCallback(
    (groupName: string) => {
      emitChange([
        ...rules,
        {
          _id: uid(),
          userGroup: groupName,
          visible: true,
          targetGroup: '',
          description: '',
        },
      ])
    },
    [rules, emitChange]
  )

  const grouped = useMemo(() => {
    const map: Record<string, Rule[]> = {}
    const order: string[] = []
    for (const r of rules) {
      if (!r.userGroup) continue
      if (!map[r.userGroup]) {
        map[r.userGroup] = []
        order.push(r.userGroup)
      }
      map[r.userGroup].push(r)
    }
    return order.map((name) => ({ name, items: map[name] }))
  }, [rules])

  const newGroupCandidates = useMemo(() => {
    const used = new Set(grouped.map((g) => g.name))
    return props.groupOptions.filter((name) => !used.has(name))
  }, [grouped, props.groupOptions])

  return (
    <Card className={sectionCardClassName}>
      <CardHeader className={sectionHeaderClassName}>
        <CardTitle>{t('Special usable group rules')}</CardTitle>
        <CardDescription>
          {t(
            'Make extra groups visible to, or hide default groups from, users of a specific group.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className='space-y-3'>
          {grouped.length === 0 ? (
            <EmptyState
              className='min-h-40'
              title={t('No rules yet. Add a group below to get started.')}
            />
          ) : (
            grouped.map((group) => (
              <GroupSection
                key={group.name}
                groupName={group.name}
                items={group.items}
                groupOptions={props.groupOptions}
                onUpdate={updateRule}
                onRemove={removeRule}
                onAdd={addRuleToGroup}
                onRemoveGroup={removeGroup}
              />
            ))
          )}

          <div className='flex items-center justify-center pt-2'>
            <GroupSelect
              className='w-full sm:w-72'
              options={newGroupCandidates}
              value=''
              placeholder={t('Add rules for a user group')}
              onValueChange={addRuleToGroup}
            />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
