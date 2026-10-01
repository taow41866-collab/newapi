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
import type { ChannelAffinitySettings } from '../general/channel-affinity/types'
import type { SecuritySettings } from '../types'

export type RetrySettings = {
  RetryTimes: number
  AutomaticRetryStatusCodes: string
}
export type HealthSettings = {
  ChannelDisableThreshold: string
  AutomaticDisableChannelEnabled: boolean
  AutomaticEnableChannelEnabled: boolean
  AutomaticDisableKeywords: string
  AutomaticDisableStatusCodes: string
  'monitor_setting.auto_test_channel_enabled': boolean
  'monitor_setting.auto_test_channel_minutes': number
  'monitor_setting.channel_test_concurrency': number
  'monitor_setting.channel_test_mode':
    | 'scheduled_all'
    | 'auto_ban_only'
    | 'passive_recovery'
}
export type ProbeSettings = {
  'probe_setting.platform_models': string
  'probe_setting.openai_reliable_enabled': boolean
  'probe_setting.openai_reasoning_effort': 'low' | 'medium' | 'high' | 'xhigh'
  'probe_setting.scheduling_protection_enabled': boolean
  'probe_setting.scheduling_failure_threshold': number
  'probe_setting.scheduling_success_threshold': number
  'probe_setting.append_probe_error_codes': boolean
  'probe_setting.first_token_protection_enabled': boolean
  'probe_setting.first_token_threshold_seconds': number
  'probe_setting.first_token_minimum_samples': number
}
export type FilteringSettings = Pick<
  SecuritySettings,
  'CheckSensitiveEnabled' | 'CheckSensitiveOnPromptEnabled' | 'SensitiveWords'
>
export type RequestPolicySettings = RetrySettings &
  HealthSettings &
  ProbeSettings &
  FilteringSettings &
  Pick<ChannelAffinitySettings, keyof ChannelAffinitySettings>

export const defaultRequestPolicySettings: RequestPolicySettings = {
  RetryTimes: 0,
  AutomaticRetryStatusCodes:
    '100-199,300-399,401-407,409-499,500-503,505-523,525-599',
  ChannelDisableThreshold: '',
  AutomaticDisableChannelEnabled: false,
  AutomaticEnableChannelEnabled: false,
  AutomaticDisableKeywords: '',
  AutomaticDisableStatusCodes: '401',
  'monitor_setting.auto_test_channel_enabled': false,
  'monitor_setting.auto_test_channel_minutes': 10,
  'monitor_setting.channel_test_concurrency': 1,
  'monitor_setting.channel_test_mode': 'scheduled_all',
  'probe_setting.platform_models': '{}',
  'probe_setting.openai_reliable_enabled': false,
  'probe_setting.openai_reasoning_effort': 'high',
  'probe_setting.scheduling_protection_enabled': false,
  'probe_setting.scheduling_failure_threshold': 2,
  'probe_setting.scheduling_success_threshold': 2,
  'probe_setting.append_probe_error_codes': false,
  'probe_setting.first_token_protection_enabled': false,
  'probe_setting.first_token_threshold_seconds': 45,
  'probe_setting.first_token_minimum_samples': 5,
  'channel_affinity_setting.enabled': false,
  'channel_affinity_setting.session_mode': '',
  'channel_affinity_setting.switch_on_success': true,
  'channel_affinity_setting.keep_on_channel_disabled': false,
  'channel_affinity_setting.max_entries': 100000,
  'channel_affinity_setting.default_ttl_seconds': 3600,
  'channel_affinity_setting.rules': '[]',
  CheckSensitiveEnabled: false,
  CheckSensitiveOnPromptEnabled: false,
  SensitiveWords: '',
}
