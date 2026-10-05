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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Save, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { formatTimestampToDate } from '@/lib/format'
import { markServerErrorHandled } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import {
  getServerErrorMessage,
  getServerErrorStatus,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  getCustomerChannelDiscounts,
  updateCustomerChannelDiscounts,
} from '../api'
import {
  changedCustomerDiscounts,
  currentCustomerDiscounts,
  customerDiscountEditorSchema,
  type CustomerDiscountFormValues,
} from '../lib/customer-discounts'
import type { CustomerChannelDiscountHistory } from '../types'

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
  userId: number
  username: string
}

export function CustomerChannelDiscountDialog(props: Props) {
  const { t } = useTranslation()
  const isRoot = useAuthStore(
    (state) => state.auth.user?.role === ROLE.SUPER_ADMIN
  )
  const [saved, setSaved] = useState(false)
  const history = useQuery({
    queryKey: ['customer-channel-discounts', props.userId],
    queryFn: () => getCustomerChannelDiscounts(props.userId),
    enabled: props.open && isRoot,
    staleTime: 0,
    refetchOnMount: true,
    refetchOnWindowFocus: false,
    meta: { errorToast: false },
  })
  if (!isRoot) return null

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Customer channel discounts')}
      description={`${props.username} (ID: ${props.userId})`}
      contentClassName='sm:max-w-3xl'
    >
      {history.isPending && <LoadingState />}
      {history.isError && (
        <ErrorState
          description={getServerErrorMessage(
            history.error,
            t('Failed to load customer discounts')
          )}
          onRetry={() => {
            void history.refetch()
          }}
        />
      )}
      {history.data && !history.isError && (
        <>
          {saved && <p role='status'>{t('Customer discounts saved')}</p>}
          <DiscountEditor
            key={`${props.userId}/${history.data.version}`}
            userId={props.userId}
            history={history.data}
            onSaved={() => setSaved(true)}
            onRefresh={async () => {
              const result = await history.refetch()
              if (result.error) {
                throw result.error
              }
              if (!result.data) {
                throw new Error(t('Failed to load customer discounts'))
              }
              return result.data
            }}
          />
        </>
      )}
    </Dialog>
  )
}

function DiscountEditor(props: {
  userId: number
  history: CustomerChannelDiscountHistory
  onSaved: () => void
  onRefresh: () => Promise<CustomerChannelDiscountHistory>
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const current = currentCustomerDiscounts(props.history.rules)
  const [error, setError] = useState('')
  const [conflict, setConflict] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const form = useForm<CustomerDiscountFormValues>({
    resolver: zodResolver(
      customerDiscountEditorSchema(current, props.history.rules.length)
    ),
    defaultValues: {
      rules: current.map((rule) => ({
        channel_id: rule.channel_id,
        model: rule.model,
        multiplier: rule.multiplier,
      })),
    },
  })
  const array = useFieldArray({ control: form.control, name: 'rules' })
  const changeCount = changedCustomerDiscounts(
    current,
    form.watch('rules')
  ).length
  const mutation = useMutation({
    mutationFn: (values: CustomerDiscountFormValues) => {
      const changes = changedCustomerDiscounts(current, values.rules)
      if (changes.length > 100) {
        throw new Error(t('Save no more than 100 rule changes at a time'))
      }
      if (props.history.rules.length + changes.length > 1000) {
        throw new Error(t('Customer discount history limit reached'))
      }
      return updateCustomerChannelDiscounts(
        props.userId,
        props.history.version,
        changes
      )
    },
    onSuccess: (history) => {
      setError('')
      props.onSaved()
      queryClient.setQueryData(
        ['customer-channel-discounts', props.userId],
        history
      )
    },
    onError: (failure) => {
      markServerErrorHandled(failure)
      const changed = getServerErrorStatus(failure) === 409
      setConflict(changed)
      setError(
        changed
          ? t('Customer discounts changed. Refresh before saving again.')
          : getServerErrorMessage(
              failure,
              t('Failed to save customer discounts')
            )
      )
    },
    meta: { errorToast: false },
  })
  const busy = mutation.isPending || refreshing

  async function refreshRules() {
    setRefreshing(true)
    try {
      const history = await props.onRefresh()
      form.reset({
        rules: currentCustomerDiscounts(history.rules).map((rule) => ({
          channel_id: rule.channel_id,
          model: rule.model,
          multiplier: rule.multiplier,
        })),
      })
      setConflict(false)
      setError('')
    } catch (failure) {
      markServerErrorHandled(failure)
      setError(
        getServerErrorMessage(failure, t('Failed to load customer discounts'))
      )
    } finally {
      setRefreshing(false)
    }
  }

  return (
    <div className='flex min-w-0 flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Wallet only. Exact model rules override channel defaults; discounts do not stack. Subscriptions and channel access are unchanged.'
          )}
        </AlertDescription>
      </Alert>
      {error && (
        <Alert variant='destructive' role='alert'>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {conflict && (
        <Button
          type='button'
          variant='outline'
          disabled={busy}
          onClick={() => {
            void refreshRules()
          }}
        >
          {t('Refresh rules')}
        </Button>
      )}
      <Form {...form}>
        <form
          onSubmit={form.handleSubmit((values) => {
            if (changedCustomerDiscounts(current, values.rules).length === 0) {
              props.onSaved()
              return
            }
            mutation.mutate(values)
          })}
          className='flex min-w-0 flex-col gap-4'
          noValidate
        >
          {array.fields.length === 0 && (
            <EmptyState
              title={t('No customer discounts')}
              className='min-h-24'
            />
          )}
          <div className='flex max-h-[50vh] min-w-0 flex-col gap-3 overflow-y-auto'>
            {array.fields.map((item, index) => (
              <div
                key={item.id}
                className='grid min-w-0 grid-cols-1 items-start gap-2 sm:grid-cols-[7rem_minmax(0,1fr)_8rem_auto]'
              >
                <FormField
                  control={form.control}
                  name={`rules.${index}.channel_id`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Channel ID {{index}}', { index: index + 1 })}
                      </FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          step={1}
                          {...field}
                          value={field.value || ''}
                          disabled={busy || conflict}
                          onChange={(event) =>
                            field.onChange(Number(event.target.value))
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name={`rules.${index}.model`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Model {{index}}', { index: index + 1 })}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          maxLength={255}
                          disabled={busy || conflict}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name={`rules.${index}.multiplier`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Discount multiplier {{index}}', {
                          index: index + 1,
                        })}
                      </FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={0}
                          max={1}
                          step='any'
                          {...field}
                          value={Number.isNaN(field.value) ? '' : field.value}
                          disabled={busy || conflict}
                          onChange={(event) =>
                            field.onChange(
                              event.target.value === ''
                                ? Number.NaN
                                : Number(event.target.value)
                            )
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button
                        type='button'
                        variant='ghost'
                        size='icon'
                        aria-label={t('Remove rule {{index}}', {
                          index: index + 1,
                        })}
                        className='sm:mt-6'
                        disabled={busy || conflict}
                        onClick={() => array.remove(index)}
                      />
                    }
                  >
                    <Trash2 />
                  </TooltipTrigger>
                  <TooltipContent>{t('Remove discount rule')}</TooltipContent>
                </Tooltip>
              </div>
            ))}
          </div>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <Button
              type='button'
              variant='outline'
              disabled={
                busy ||
                conflict ||
                array.fields.length >= 1000 ||
                changeCount >= 100 ||
                props.history.rules.length + changeCount >= 1000
              }
              onClick={() =>
                array.append({ channel_id: 0, model: '*', multiplier: 1 })
              }
            >
              <Plus data-icon='inline-start' />
              {t('Add discount rule')}
            </Button>
            <Button type='submit' disabled={busy || conflict}>
              <Save data-icon='inline-start' />
              {busy ? t('Saving...') : t('Save customer discounts')}
            </Button>
          </div>
          {form.formState.errors.rules?.root?.message && (
            <p role='alert' className='text-destructive text-sm'>
              {t(form.formState.errors.rules.root.message)}
            </p>
          )}
        </form>
      </Form>
      <details>
        <summary className='cursor-pointer text-sm'>
          {t('Rule history')}
        </summary>
        <div className='mt-2 max-h-64 overflow-auto'>
          <Table className='text-xs'>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Channel')}</TableHead>
                <TableHead>{t('Model')}</TableHead>
                <TableHead>{t('Multiplier')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
                <TableHead>{t('Version')}</TableHead>
                <TableHead>{t('Effective at')}</TableHead>
                <TableHead>{t('Operator')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {[...props.history.rules].reverse().map((rule) => (
                <TableRow
                  key={`${rule.version}/${rule.channel_id}/${rule.model}`}
                >
                  <TableCell>{rule.channel_id}</TableCell>
                  <TableCell className='max-w-60 break-all'>
                    {rule.model}
                  </TableCell>
                  <TableCell>{rule.multiplier}</TableCell>
                  <TableCell>
                    {rule.disabled ? t('Disabled') : t('Enabled')}
                  </TableCell>
                  <TableCell>{rule.version}</TableCell>
                  <TableCell className='whitespace-nowrap'>
                    {formatTimestampToDate(rule.effective_at, 'milliseconds')}
                  </TableCell>
                  <TableCell>{rule.actor_id}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </details>
    </div>
  )
}
