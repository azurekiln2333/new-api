import { createContext } from 'react'

import type { ChannelUsageSummary, StatisticsCoverage } from '../types'

export interface ChannelUsageContextValue {
  usageByChannel: Record<number, ChannelUsageSummary>
  coverage?: StatisticsCoverage
}

export const ChannelUsageContext = createContext<ChannelUsageContextValue>({
  usageByChannel: {},
})
