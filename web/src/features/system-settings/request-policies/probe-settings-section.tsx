import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { getChannels } from '@/features/channels/api'
import type { Channel } from '@/features/channels/types'
import { handleServerError } from '@/lib/handle-server-error'

import {
  SettingsControlGroup,
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { safeNumberFieldProps } from '../utils/numeric-field'
import type { ProbeSettings } from './defaults'
import { useSavePolicy } from './use-save-policy'

const platforms = [
  { key: 'openai', label: 'OpenAI', types: [1, 6, 57] },
  { key: 'anthropic', label: 'Anthropic', types: [14] },
  { key: 'gemini', label: 'Gemini', types: [24, 41] },
  { key: 'grok', label: 'Grok', types: [48] },
  { key: 'kimi', label: 'Kimi', types: [25] },
  { key: 'zhipu', label: 'Zhipu GLM', types: [16, 26] },
  { key: 'deepseek', label: 'DeepSeek', types: [43] },
  { key: 'minimax', label: 'MiniMax', types: [35] },
] as const

type PlatformKey = (typeof platforms)[number]['key']
type ProbeFormValues = {
  platform_models: Record<PlatformKey, string>
  openai_reliable_enabled: boolean
  openai_reasoning_effort: 'low' | 'medium' | 'high' | 'xhigh'
  scheduling_protection_enabled: boolean
  scheduling_failure_threshold: number
  scheduling_success_threshold: number
  append_probe_error_codes: boolean
  first_token_protection_enabled: boolean
  first_token_threshold_seconds: number
  first_token_minimum_samples: number
}

const formSchema = z.object({
  platform_models: z.record(z.string(), z.string()),
  openai_reliable_enabled: z.boolean(),
  openai_reasoning_effort: z.enum(['low', 'medium', 'high', 'xhigh']),
  scheduling_protection_enabled: z.boolean(),
  scheduling_failure_threshold: z.number().int().min(1).max(20),
  scheduling_success_threshold: z.number().int().min(1).max(20),
  append_probe_error_codes: z.boolean(),
  first_token_protection_enabled: z.boolean(),
  first_token_threshold_seconds: z.number().int().min(5).max(300),
  first_token_minimum_samples: z.number().int().min(2).max(20),
})

const fallbackModels: Record<PlatformKey, string[]> = {
  openai: ['gpt-6.1-sol'],
  anthropic: ['claude-sonnet-5'],
  gemini: ['gemini-3.8-flash'],
  grok: ['grok-4.7'],
  kimi: ['kimi-k3'],
  zhipu: ['glm-5.3'],
  deepseek: ['deepseek-v4.1-flash'],
  minimax: ['MiniMax-M2'],
}

function parseModels(value: string): Record<PlatformKey, string> {
  let parsed: Record<string, unknown> = {}
  try {
    const candidate = JSON.parse(value)
    if (
      candidate &&
      typeof candidate === 'object' &&
      !Array.isArray(candidate)
    ) {
      parsed = candidate as Record<string, unknown>
    }
  } catch {
    // Older installations may not have this option yet.
  }
  return Object.fromEntries(
    platforms.map(({ key }) => [
      key,
      typeof parsed[key] === 'string' ? parsed[key] : '',
    ])
  ) as Record<PlatformKey, string>
}

function channelModels(channel: Channel): string[] {
  const configured = [channel.test_model ?? '']
  configured.push(...channel.models.split(/[\n,]/g))
  return [
    ...new Set(configured.map((model) => model.trim()).filter(Boolean)),
  ].slice(0, 20)
}

function normalizePlatformModels(
  values: Record<PlatformKey, string>
): Record<PlatformKey, string> {
  return Object.fromEntries(
    platforms.map(({ key }) => [key, values[key].trim()])
  ) as Record<PlatformKey, string>
}

function persistedPlatformModels(
  values: Record<PlatformKey, string>
): Record<string, string> {
  return Object.fromEntries(
    platforms
      .map(({ key }) => [key, values[key].trim()] as const)
      .filter(([, value]) => value !== '')
  )
}

function normalizeFormValues(values: ProbeFormValues): ProbeFormValues {
  return {
    ...values,
    platform_models: normalizePlatformModels(values.platform_models),
  }
}

export function ProbeSettingsSection({
  defaultValues,
}: {
  defaultValues: ProbeSettings
}) {
  const { t } = useTranslation()
  const updateOption = useSavePolicy()
  const [channels, setChannels] = useState<Channel[]>([])
  const baselineRef = useRef<ProbeFormValues | null>(null)
  const formDefaults = useMemo<ProbeFormValues>(
    () => ({
      platform_models: parseModels(
        defaultValues['probe_setting.platform_models']
      ),
      openai_reliable_enabled:
        defaultValues['probe_setting.openai_reliable_enabled'],
      openai_reasoning_effort:
        defaultValues['probe_setting.openai_reasoning_effort'],
      scheduling_protection_enabled:
        defaultValues['probe_setting.scheduling_protection_enabled'],
      scheduling_failure_threshold:
        defaultValues['probe_setting.scheduling_failure_threshold'],
      scheduling_success_threshold:
        defaultValues['probe_setting.scheduling_success_threshold'],
      append_probe_error_codes:
        defaultValues['probe_setting.append_probe_error_codes'],
      first_token_protection_enabled:
        defaultValues['probe_setting.first_token_protection_enabled'],
      first_token_threshold_seconds:
        defaultValues['probe_setting.first_token_threshold_seconds'],
      first_token_minimum_samples:
        defaultValues['probe_setting.first_token_minimum_samples'],
    }),
    [defaultValues]
  )
  const form = useForm<ProbeFormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: formDefaults,
  })

  useResetForm(form, formDefaults)
  useEffect(() => {
    baselineRef.current = normalizeFormValues(formDefaults)
  }, [formDefaults])
  useEffect(() => {
    let active = true
    getChannels({ p: 1, page_size: 1000 })
      .then((response) => {
        if (active) setChannels(response.data?.items ?? [])
      })
      .catch(() => {
        if (active) setChannels([])
      })
    return () => {
      active = false
    }
  }, [])

  const optionsByPlatform = useMemo(() => {
    const result = {} as Record<PlatformKey, string[]>
    for (const platform of platforms) {
      const discovered = channels
        .filter((channel) =>
          platform.types.some((type) => type === channel.type)
        )
        .flatMap(channelModels)
      result[platform.key] = [
        ...new Set([...discovered, ...fallbackModels[platform.key]]),
      ]
    }
    return result
  }, [channels])

  const onSubmit = async (values: ProbeFormValues) => {
    const normalized = normalizeFormValues(values)
    const baseline = baselineRef.current
    if (!baseline) return
    const changed =
      JSON.stringify(persistedPlatformModels(normalized.platform_models)) !==
        JSON.stringify(persistedPlatformModels(baseline.platform_models)) ||
      normalized.openai_reliable_enabled !== baseline.openai_reliable_enabled ||
      normalized.openai_reasoning_effort !== baseline.openai_reasoning_effort ||
      normalized.scheduling_protection_enabled !==
        baseline.scheduling_protection_enabled ||
      normalized.scheduling_failure_threshold !==
        baseline.scheduling_failure_threshold ||
      normalized.scheduling_success_threshold !==
        baseline.scheduling_success_threshold ||
      normalized.append_probe_error_codes !==
        baseline.append_probe_error_codes ||
      normalized.first_token_protection_enabled !==
        baseline.first_token_protection_enabled ||
      normalized.first_token_threshold_seconds !==
        baseline.first_token_threshold_seconds ||
      normalized.first_token_minimum_samples !==
        baseline.first_token_minimum_samples
    if (!changed) {
      toast.info(t('No changes to save'))
      return
    }
    try {
      await updateOption.mutateAsync({
        'probe_setting.platform_models': JSON.stringify(
          persistedPlatformModels(normalized.platform_models)
        ),
        'probe_setting.openai_reliable_enabled': String(
          normalized.openai_reliable_enabled
        ),
        'probe_setting.openai_reasoning_effort':
          normalized.openai_reasoning_effort,
        'probe_setting.scheduling_protection_enabled': String(
          normalized.scheduling_protection_enabled
        ),
        'probe_setting.scheduling_failure_threshold': String(
          normalized.scheduling_failure_threshold
        ),
        'probe_setting.scheduling_success_threshold': String(
          normalized.scheduling_success_threshold
        ),
        'probe_setting.append_probe_error_codes': String(
          normalized.append_probe_error_codes
        ),
        'probe_setting.first_token_protection_enabled': String(
          normalized.first_token_protection_enabled
        ),
        'probe_setting.first_token_threshold_seconds': String(
          normalized.first_token_threshold_seconds
        ),
        'probe_setting.first_token_minimum_samples': String(
          normalized.first_token_minimum_samples
        ),
      })
      baselineRef.current = normalized
    } catch (error) {
      handleServerError(error)
    }
  }

  return (
    <SettingsSection title={t('Probe settings')}>
      <div className='text-muted-foreground space-y-1 text-sm'>
        <p>
          {t(
            'Probe settings are applied by the existing scheduled channel health task.'
          )}
        </p>
        <p>
          {t('Empty model selections fall back to each channel test model.')}
        </p>
      </div>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={form.formState.isSubmitting}
          />

          <SettingsControlGroup>
            <h4 className='text-sm font-medium'>
              {t('Platform probe models')}
            </h4>
            <p className='text-muted-foreground text-xs'>
              {t('Candidates come from configured channels and safe defaults.')}
            </p>
            <div className='grid gap-x-5 gap-y-4 sm:grid-cols-2 lg:grid-cols-3'>
              {platforms.map((platform) => (
                <FormItem key={platform.key}>
                  <FormLabel>{platform.label}</FormLabel>
                  <Select
                    value={form.watch(`platform_models.${platform.key}`)}
                    onValueChange={(value) =>
                      form.setValue(
                        `platform_models.${platform.key}`,
                        value == null || value === '__empty__' ? '' : value,
                        {
                          shouldDirty: true,
                        }
                      )
                    }
                  >
                    <FormControl>
                      <SelectTrigger className='w-full'>
                        <SelectValue
                          placeholder={t('Use channel test model')}
                        />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent>
                      <SelectItem value='__empty__'>
                        {t('Use channel test model')}
                      </SelectItem>
                      {optionsByPlatform[platform.key].map((model) => (
                        <SelectItem key={model} value={model}>
                          {model}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </FormItem>
              ))}
            </div>
          </SettingsControlGroup>

          <SettingsControlGroup>
            <FormField
              control={form.control}
              name='openai_reliable_enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('OpenAI reliable probe')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Use one Responses request to verify OpenAI probe consistency.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
            <div className='grid gap-x-5 gap-y-4 sm:grid-cols-2'>
              <FormField
                control={form.control}
                name='openai_reasoning_effort'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Reasoning effort')}</FormLabel>
                    <Select value={field.value} onValueChange={field.onChange}>
                      <FormControl>
                        <SelectTrigger className='w-full'>
                          <SelectValue />
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        {(['low', 'medium', 'high', 'xhigh'] as const).map(
                          (effort) => (
                            <SelectItem key={effort} value={effort}>
                              {effort}
                            </SelectItem>
                          )
                        )}
                      </SelectContent>
                    </Select>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </SettingsControlGroup>

          <Separator />

          <SettingsControlGroup>
            <FormField
              control={form.control}
              name='scheduling_protection_enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Probe scheduling protection')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Require consecutive probe failures or successes before changing channel status.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
            <div className='grid gap-x-5 gap-y-4 sm:grid-cols-2'>
              <FormField
                control={form.control}
                name='scheduling_failure_threshold'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Consecutive failures')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        max={20}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Allowed range: 1-20')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='scheduling_success_threshold'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Consecutive recoveries')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={1}
                        max={20}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Allowed range: 1-20')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </SettingsControlGroup>

          <FormField
            control={form.control}
            name='append_probe_error_codes'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Append probe error codes')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Treat upstream probe errors as disable candidates when automatic disabling is enabled.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <Separator />

          <SettingsControlGroup>
            <FormField
              control={form.control}
              name='first_token_protection_enabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>
                      {t('First response byte performance protection')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'Quarantine channels whose latest streaming first-byte samples average above the threshold.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
            <div className='grid gap-x-5 gap-y-4 sm:grid-cols-2'>
              <FormField
                control={form.control}
                name='first_token_threshold_seconds'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Threshold (seconds)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={5}
                        max={300}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Allowed range: 5-300')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='first_token_minimum_samples'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Minimum samples')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={2}
                        max={20}
                        {...safeNumberFieldProps(field)}
                      />
                    </FormControl>
                    <FormDescription>
                      {t('Allowed range: 2-20')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </SettingsControlGroup>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
