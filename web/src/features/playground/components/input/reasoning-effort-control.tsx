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
import { BrainCircuitIcon, RotateCcwIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { PromptInputButton } from '@/components/ai-elements/prompt-input'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Slider } from '@/components/ui/slider'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import type { ReasoningEffort } from '../../types'

const REASONING_EFFORTS = ['minimal', 'low', 'medium', 'high', 'xhigh'] as const

const REASONING_EFFORT_LABELS: Record<ReasoningEffort, string> = {
  minimal: 'Minimal reasoning',
  low: 'Light reasoning',
  medium: 'Balanced reasoning',
  high: 'Heavy reasoning',
  xhigh: 'Extra-high reasoning',
}

type ReasoningEffortControlProps = {
  disabled?: boolean
  onChange: (value: ReasoningEffort | null) => void
  value: ReasoningEffort | null
}

export function ReasoningEffortControl(props: ReasoningEffortControlProps) {
  const { t } = useTranslation()
  const selectedIndex = props.value ? REASONING_EFFORTS.indexOf(props.value) : 2
  const currentLabel = props.value
    ? t(REASONING_EFFORT_LABELS[props.value])
    : t('Automatic')

  return (
    <Popover>
      <Tooltip>
        <TooltipTrigger
          render={
            <PopoverTrigger
              render={
                <PromptInputButton
                  aria-label={t('Adjust reasoning effort')}
                  className='text-muted-foreground hover:text-foreground hover:bg-muted/70 gap-1.5 font-medium'
                  disabled={props.disabled}
                  variant='ghost'
                >
                  <BrainCircuitIcon aria-hidden='true' size={16} />
                  <span className='max-w-20 truncate text-xs'>
                    {currentLabel}
                  </span>
                </PromptInputButton>
              }
            />
          }
        />
        <TooltipContent>
          <p>{t('Reasoning effort')}</p>
        </TooltipContent>
      </Tooltip>
      <PopoverContent
        align='start'
        className='w-64 gap-3 p-3'
        collisionPadding={8}
        side='top'
        sideOffset={8}
      >
        <div className='flex items-center justify-between gap-3'>
          <div className='flex min-w-0 items-center gap-2'>
            <BrainCircuitIcon
              aria-hidden='true'
              className='text-muted-foreground size-4 shrink-0'
            />
            <span className='truncate text-sm font-medium'>
              {t('Reasoning effort')}
            </span>
          </div>
          <span className='text-primary max-w-28 truncate text-xs font-medium'>
            {currentLabel}
          </span>
          <Tooltip>
            <TooltipTrigger
              render={
                <PromptInputButton
                  aria-label={t('Reset reasoning effort')}
                  className='text-muted-foreground hover:text-foreground size-7'
                  disabled={props.disabled || props.value === null}
                  onClick={() => props.onChange(null)}
                  variant='ghost'
                >
                  <RotateCcwIcon aria-hidden='true' size={14} />
                </PromptInputButton>
              }
            />
            <TooltipContent>
              <p>{t('Reset reasoning effort')}</p>
            </TooltipContent>
          </Tooltip>
        </div>

        <Slider
          className='py-1.5'
          disabled={props.disabled}
          max={REASONING_EFFORTS.length - 1}
          min={0}
          onValueChange={(nextValue) => {
            const index = Array.isArray(nextValue) ? nextValue[0] : nextValue
            const effort = REASONING_EFFORTS[index]
            if (effort) props.onChange(effort)
          }}
          step={1}
          thumbAriaLabel={t('Reasoning effort')}
          thumbAriaValueText={currentLabel}
          value={[selectedIndex]}
        />
        <div
          aria-hidden='true'
          className='text-muted-foreground -mt-2 flex justify-between text-[11px]'
        >
          <span>{t('Light reasoning')}</span>
          <span>{t('Heavy reasoning')}</span>
        </div>
      </PopoverContent>
    </Popover>
  )
}
