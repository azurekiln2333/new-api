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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import { useLayoutEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { PasswordInput } from '@/components/password-input'
import { PasswordStrength } from '@/components/password-strength'
import { PermissionMatrix } from '@/components/permission-matrix'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
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
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  EMPTY_PERMISSION_CATALOG,
  hasPermission,
  normalizeAdminPermissions,
  type AdminPermissionMatrix,
} from '@/lib/admin-permissions'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import {
  formatQuota,
  getEditableQuotaStep,
  parseQuotaFromDollars,
  quotaUnitsToDollars,
} from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { accountPasswordSchema } from '@/lib/password-policy'
import { ROLE } from '@/lib/roles'
import { AuthOperationError } from '@/lib/secure-verification'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  createUser,
  updateUser,
  getUser,
  getGroups,
  getPermissionCatalog,
  adjustUserQuota,
} from '../api'
import { BINDING_FIELDS, ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import {
  userFormSchema,
  type UserFormValues,
  USER_FORM_DEFAULT_VALUES,
  transformFormDataToPayload,
  transformUserToFormDefaults,
} from '../lib'
import type { User } from '../types'
import { UserRateLimitSection } from './user-rate-limit-section'
import { useUsers } from './users-provider'

type UsersMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: User
}

export function UsersMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: UsersMutateDrawerProps) {
  const { t } = useTranslation()
  const isUpdate = !!currentRow
  const { triggerRefresh, requestVerification } = useUsers()
  const currentUser = useAuthStore((s) => s.auth.user)
  const [isSubmitting, setIsSubmitting] = useState(false)
  // Matrix as loaded from the server; an unchanged matrix is not resubmitted so
  // routine edits of an administrator do not require step-up verification.
  const loadedPermissions = useRef<AdminPermissionMatrix | undefined>(undefined)
  const activeUserId = useRef<number | undefined>(undefined)
  const loadedQuota = useRef<{ id: number; value: number } | undefined>(
    undefined
  )
  const [userLoadState, setUserLoadState] = useState<{
    id: number | undefined
    status: 'idle' | 'loading' | 'ready' | 'error'
  }>({ id: undefined, status: 'idle' })
  const [reloadUser, setReloadUser] = useState(0)
  const [isAdjustingQuota, setIsAdjustingQuota] = useState(false)
  // A state update is not visible to a click handler fired in the same tick, so
  // the button label alone cannot stop a double submit of the quota change.
  const quotaSubmitLock = useRef(false)

  // Fetch groups
  const { data: groupsData } = useQuery({
    queryKey: ['groups'],
    queryFn: async () => requireServerSuccess(await getGroups()),
    staleTime: 5 * 60 * 1000,
  })

  const groups = groupsData?.data || []

  // Permission catalog is owned by the backend; fetched once and reused.
  const { data: permissionCatalog = EMPTY_PERMISSION_CATALOG } = useQuery({
    queryKey: ['admin-permission-catalog'],
    queryFn: async () => requireServerSuccess(await getPermissionCatalog()),
    staleTime: 5 * 60 * 1000,
  })

  const form = useForm<UserFormValues>({
    resolver: zodResolver(userFormSchema),
    defaultValues: USER_FORM_DEFAULT_VALUES,
  })

  // Read the latest list projection only when the reset identity changes.
  const listRow = useRef(currentRow)
  useLayoutEffect(() => {
    listRow.current = currentRow
  }, [currentRow])

  // Replace the visible form synchronously when its identity changes. The
  // cleanup fences late detail responses from prior users and close/reopen.
  useLayoutEffect(() => {
    let active = true
    const row = listRow.current
    const targetId = row?.id
    activeUserId.current = open ? targetId : undefined

    if (!open) {
      form.reset(USER_FORM_DEFAULT_VALUES)
      loadedPermissions.current = undefined
      loadedQuota.current = undefined
      setUserLoadState({ id: undefined, status: 'idle' })
      return () => {
        active = false
      }
    }

    if (!isUpdate || !row) {
      form.reset(USER_FORM_DEFAULT_VALUES)
      loadedPermissions.current = undefined
      loadedQuota.current = undefined
      setUserLoadState({ id: undefined, status: 'ready' })
      return () => {
        active = false
      }
    }

    form.reset(transformUserToFormDefaults(row))
    loadedPermissions.current = undefined
    loadedQuota.current = { id: row.id, value: row.quota }
    setUserLoadState({ id: targetId, status: 'loading' })

    void getUser(row.id)
      .then((result) => {
        if (!active) return
        if (!result.success || !result.data || result.data.id !== targetId) {
          setUserLoadState({ id: targetId, status: 'error' })
          handleServerError(result, t('Failed to load'))
          return
        }

        // The baseline comes from this detail response, never from the list
        // row: an unchanged matrix must compare against what the server holds.
        loadedPermissions.current = result.data.admin_permissions
        loadedQuota.current = {
          id: result.data.id,
          value: result.data.quota,
        }
        form.reset(transformUserToFormDefaults(result.data))
        setUserLoadState({ id: targetId, status: 'ready' })
      })
      .catch((error: unknown) => {
        if (!active) return
        setUserLoadState({ id: targetId, status: 'error' })
        handleServerError(error, t('Failed to load'))
      })

    return () => {
      active = false
    }
  }, [open, isUpdate, currentRow?.id, form, t, reloadUser])

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'

  const currentQuotaRaw = form.watch('quota_dollars') || 0
  const selectedRole = form.watch('role')
  const isUserReady =
    !isUpdate ||
    (userLoadState.id === currentRow?.id && userLoadState.status === 'ready')
  const isUserLoading =
    isUpdate &&
    userLoadState.id === currentRow?.id &&
    userLoadState.status === 'loading'
  const isUserLoadError =
    isUpdate &&
    userLoadState.id === currentRow?.id &&
    userLoadState.status === 'error'
  const canEditAdminPermissions = currentUser?.role === ROLE.SUPER_ADMIN
  const targetIsAdmin = (selectedRole ?? currentRow?.role ?? 0) >= ROLE.ADMIN

  const onSubmit = async (data: UserFormValues) => {
    const targetId = currentRow?.id
    if (!isUserReady || (isUpdate && targetId === undefined)) return

    if (!isUpdate || data.password) {
      if (!accountPasswordSchema.safeParse(data.password ?? '').success) {
        form.setError('password', {
          type: 'manual',
          message: t('Password must contain between 8 and 128 characters.'),
        })
        return
      }
    }

    setIsSubmitting(true)
    try {
      const payload = transformFormDataToPayload(
        data,
        targetId,
        permissionCatalog
      )
      if (
        isUpdate &&
        payload.admin_permissions &&
        JSON.stringify(payload.admin_permissions) ===
          JSON.stringify(
            normalizeAdminPermissions(
              loadedPermissions.current,
              permissionCatalog
            )
          )
      ) {
        delete payload.admin_permissions
      }
      // Resetting a password, rewriting admin permissions, or creating an
      // administrator changes who can sign in or what they may do.
      let proofToken: string | undefined
      if (isUpdate && currentRow) {
        if (payload.password || payload.admin_permissions) {
          const proof = await requestVerification({
            scope: 'admin.user.update',
            context: { user_id: currentRow.id },
            title: t('Verify to update user credentials'),
            description: t(
              'Confirm your identity before changing the account {{username}}.',
              { username: currentRow.username }
            ),
          })
          if (!proof) return
          proofToken = proof.proof_token
        }
      } else if ((payload.role ?? 0) >= ROLE.ADMIN) {
        const proof = await requestVerification({
          scope: 'admin.user.create',
          context: { role: payload.role ?? ROLE.ADMIN },
          title: t('Verify to create administrator'),
          description: t(
            'Confirm your identity before creating an administrator account.'
          ),
        })
        if (!proof) return
        proofToken = proof.proof_token
      }
      if (
        isUpdate &&
        (!open || activeUserId.current !== targetId || !isUserReady)
      ) {
        return
      }
      const result = isUpdate
        ? await updateUser(
            payload as typeof payload & { id: number },
            proofToken
          )
        : await createUser(payload, proofToken)

      if (result.success) {
        toast.success(
          isUpdate
            ? t(SUCCESS_MESSAGES.USER_UPDATED)
            : t(SUCCESS_MESSAGES.USER_CREATED)
        )
        onOpenChange(false)
        triggerRefresh()
      } else {
        handleServerError(result, t(ERROR_MESSAGES.CREATE_FAILED))
      }
    } catch (error) {
      handleServerError(
        AuthOperationError.from(error),
        t(ERROR_MESSAGES.UNEXPECTED)
      )
    } finally {
      setIsSubmitting(false)
    }
  }

  const handleConfirmQuota = async () => {
    if (!currentRow || !isUserReady || quotaSubmitLock.current) return
    const targetId = currentRow.id
    const originalQuota = loadedQuota.current
    if (!originalQuota || originalQuota.id !== targetId) return
    const desiredQuota = parseQuotaFromDollars(currentQuotaRaw)
    const delta = desiredQuota - originalQuota.value
    if (delta === 0) return

    // Send the committed difference rather than an absolute balance: the server
    // applies the delta under a row lock, so concurrent consumption is kept
    // instead of being overwritten by a value the operator read earlier.
    quotaSubmitLock.current = true
    setIsAdjustingQuota(true)
    try {
      const result = await adjustUserQuota({
        id: targetId,
        action: 'add_quota',
        mode: delta > 0 ? 'add' : 'subtract',
        value: Math.abs(delta),
      })
      if (!result.success) {
        handleServerError(result, t('Failed to adjust quota'))
        return
      }

      toast.success(t('Quota adjusted successfully'))
      triggerRefresh()
      if (open && activeUserId.current === targetId) {
        // Keep the baseline on the same rounded unit the form shows, otherwise a
        // non-integral display amount would leave Confirm enabled forever.
        loadedQuota.current = {
          id: targetId,
          value: parseQuotaFromDollars(quotaUnitsToDollars(desiredQuota)),
        }
        form.resetField('quota_dollars', {
          defaultValue: quotaUnitsToDollars(desiredQuota),
        })
      }

      // Reconcile with the balance that other traffic may have moved. This is
      // best effort: the change is already committed, so a failed read must not
      // be reported as a failed adjustment.
      try {
        const refreshed = requireServerSuccess(await getUser(targetId))
        if (
          refreshed.success &&
          refreshed.data?.id === targetId &&
          open &&
          activeUserId.current === targetId
        ) {
          loadedQuota.current = {
            id: targetId,
            // Same display-unit rounding as the optimistic baseline above.
            value: parseQuotaFromDollars(
              quotaUnitsToDollars(refreshed.data.quota)
            ),
          }
          form.resetField('quota_dollars', {
            defaultValue: quotaUnitsToDollars(refreshed.data.quota),
          })
        }
      } catch {
        // Keep the reconciled-by-delta baseline shown above.
      }
    } catch (error) {
      handleServerError(error, t('Failed to adjust quota'))
    } finally {
      quotaSubmitLock.current = false
      setIsAdjustingQuota(false)
    }
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v)
        if (!v) {
          activeUserId.current = undefined
          loadedPermissions.current = undefined
          loadedQuota.current = undefined
          setUserLoadState({ id: undefined, status: 'idle' })
          form.reset(USER_FORM_DEFAULT_VALUES)
        }
      }}
    >
      <SheetContent
        className={sideDrawerContentClassName('sm:max-w-[600px]')}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isUpdate ? t('Update') : t('Create')} {t('User')}
          </SheetTitle>
          <SheetDescription>
            {isUpdate
              ? t('Update the user by providing necessary info.')
              : t('Add a new user by providing necessary info.')}
          </SheetDescription>
        </SheetHeader>
        <Form {...form}>
          {isUserLoading && <LoadingState inline message={t('Loading...')} />}
          {isUserLoadError && (
            <ErrorState
              title={t('Failed to load')}
              onRetry={() => setReloadUser((value) => value + 1)}
              className='min-h-0'
            />
          )}
          <form
            id='user-form'
            onSubmit={form.handleSubmit(onSubmit)}
            aria-busy={isUserLoading}
            inert={
              !isUserReady || isSubmitting || isAdjustingQuota
                ? true
                : undefined
            }
            className={sideDrawerFormClassName()}
          >
            {/* Basic Information */}
            <SideDrawerSection>
              <h3 className='text-sm font-medium'>{t('Basic Information')}</h3>

              <FormField
                control={form.control}
                name='username'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Username')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder={t('Enter username')}
                        disabled={isUpdate}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {!isUpdate && (
                <FormField
                  control={form.control}
                  name='role'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Role')}</FormLabel>
                      <Select
                        items={[
                          { value: '1', label: t('Common User') },
                          { value: '10', label: t('Admin') },
                        ]}
                        onValueChange={(value) =>
                          value !== null &&
                          field.onChange(Number.parseInt(value))
                        }
                        value={String(field.value)}
                      >
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue placeholder={t('Select a role')} />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent alignItemWithTrigger={false}>
                          <SelectGroup>
                            <SelectItem value='1'>
                              {t('Common User')}
                            </SelectItem>
                            <SelectItem value='10'>{t('Admin')}</SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <FormDescription>
                        {t("Set the user's role (cannot be Root)")}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}

              <FormField
                control={form.control}
                name='display_name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Display Name')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        placeholder={t('Enter display name')}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Leave empty to use username')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='password'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Password')}</FormLabel>
                    <FormControl
                      describedBy={
                        !isUpdate || field.value
                          ? 'user-password-strength'
                          : undefined
                      }
                    >
                      <PasswordInput
                        {...field}
                        value={field.value ?? ''}
                        autoComplete='new-password'
                        placeholder={
                          isUpdate
                            ? t('Leave empty to keep unchanged')
                            : t('Enter password (8–128 characters)')
                        }
                      />
                    </FormControl>
                    <PasswordStrength
                      id='user-password-strength'
                      value={field.value ?? ''}
                      quietWhenEmpty={isUpdate}
                    />
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SideDrawerSection>

            {/* Group & Quota Settings (Update only) */}
            {isUpdate && (
              <SideDrawerSection>
                <h3 className='text-sm font-medium'>{t('Group & Quota')}</h3>

                <FormField
                  control={form.control}
                  name='group'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Group')}</FormLabel>
                      <FormControl>
                        <Combobox
                          options={groups.map((group) => ({
                            value: group,
                            label: group,
                          }))}
                          onValueChange={field.onChange}
                          value={field.value}
                          className='w-full'
                          placeholder={t('Select a group')}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='quota_dollars'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Remaining Quota ({{currency}})', {
                          currency: currencyLabel,
                        })}
                      </FormLabel>
                      <div className='flex gap-2'>
                        <FormControl>
                          <Input
                            {...field}
                            type='number'
                            step={tokensOnly ? 1 : getEditableQuotaStep()}
                            value={field.value ?? 0}
                            onChange={(event) =>
                              field.onChange(
                                Number.parseFloat(event.target.value) || 0
                              )
                            }
                            className='flex-1'
                          />
                        </FormControl>
                        <Button
                          type='button'
                          variant='outline'
                          disabled={
                            !isUserReady ||
                            isAdjustingQuota ||
                            loadedQuota.current?.id !== currentRow?.id ||
                            parseQuotaFromDollars(currentQuotaRaw) ===
                              loadedQuota.current?.value
                          }
                          onClick={handleConfirmQuota}
                        >
                          {isAdjustingQuota ? t('Processing...') : t('Confirm')}
                        </Button>
                      </div>
                      <FormDescription>
                        {formatQuota(parseQuotaFromDollars(field.value || 0))}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='remark'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Remark')}</FormLabel>
                      <FormControl>
                        <Textarea
                          {...field}
                          placeholder={t(
                            'Admin notes (only visible to admins)'
                          )}
                          rows={3}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </SideDrawerSection>
            )}

            {/* Rate limiting (update only) */}
            {isUpdate && currentRow && (
              <SideDrawerSection>
                <h3 className='text-sm font-medium'>{t('Rate Limiting')}</h3>
                <UserRateLimitSection userId={currentRow.id} />
              </SideDrawerSection>
            )}

            {canEditAdminPermissions &&
              targetIsAdmin &&
              permissionCatalog.resources.length > 0 && (
                <SideDrawerSection>
                  <h3 className='text-sm font-medium'>
                    {t('Admin Permissions')}
                  </h3>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Default administrator permissions can be overridden for this user.'
                    )}
                  </p>
                  <FormField
                    control={form.control}
                    name='admin_permissions'
                    render={({ field }) => {
                      const selected = normalizeAdminPermissions(
                        field.value,
                        permissionCatalog
                      )
                      return (
                        <FormItem>
                          <PermissionMatrix
                            resources={permissionCatalog.resources}
                            value={selected}
                            onChange={field.onChange}
                          />
                          <FormMessage />
                        </FormItem>
                      )
                    }}
                  />
                  {currentUser && (
                    <p className='text-muted-foreground text-xs'>
                      {hasPermission(
                        currentUser,
                        ADMIN_PERMISSION_RESOURCES.CHANNEL,
                        ADMIN_PERMISSION_ACTIONS.SENSITIVE_WRITE
                      )
                        ? t(
                            'Your account can edit sensitive channel settings.'
                          )
                        : t(
                            'Your account cannot edit sensitive channel settings.'
                          )}
                    </p>
                  )}
                </SideDrawerSection>
              )}

            {/* Binding Information (Read-only) */}
            {isUpdate && (
              <SideDrawerSection>
                <h3 className='text-sm font-medium'>
                  {t('Binding Information')}
                </h3>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Third-party account bindings (read-only, managed by user in Security & Access)'
                  )}
                </p>

                <div className='flex flex-col gap-3'>
                  {BINDING_FIELDS.map(({ key, label }) => (
                    <div key={key}>
                      <Label className='text-muted-foreground text-xs'>
                        {t(label)}
                      </Label>
                      <Input
                        value={
                          (currentRow?.[key as keyof User] as string) || '-'
                        }
                        disabled
                        className='mt-1'
                      />
                    </div>
                  ))}
                </div>
              </SideDrawerSection>
            )}
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='user-form'
            type='submit'
            disabled={isSubmitting || !isUserReady || isAdjustingQuota}
          >
            {isSubmitting ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
