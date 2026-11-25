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
import React, { useState } from 'react'

import { CodeBatchHistoryDialog } from './code-batch-history-dialog'
import type { CodeExportScope } from './code-export-api'
import {
  CodeExportDialog,
  type CodeExportSelection,
} from './code-export-dialog'

type CodeExportContextValue = {
  /** Export an explicit selection, from the current page or across pages. */
  exportIds: (ids: number[]) => void
  /** Export every code of one historical create batch. */
  exportBatch: (operationId: string) => void
  /** Export everything matching the active filters, across all pages. */
  exportFiltered: (filters: { keyword: string; status: string }) => void
  /** Open the batch history so a lost create response can be recovered. */
  openHistory: () => void
}

const CodeExportContext = React.createContext<CodeExportContextValue | null>(
  null
)

/**
 * Hosts the shared export dialogs for one code surface. Both the invitation and
 * redemption pages mount this once, then their row menu, bulk toolbar, and page
 * header call into it, so the three selections share one implementation.
 */
export function CodeExportProvider(props: {
  scope: CodeExportScope
  children: React.ReactNode
}) {
  const [selection, setSelection] = useState<CodeExportSelection | null>(null)
  const [historyOpen, setHistoryOpen] = useState(false)

  const value: CodeExportContextValue = {
    exportIds: (ids) => {
      if (ids.length > 0) setSelection({ kind: 'ids', ids })
    },
    exportBatch: (operationId) => {
      setHistoryOpen(false)
      setSelection({ kind: 'batch', operationId })
    },
    exportFiltered: (filters) => setSelection({ kind: 'filter', ...filters }),
    openHistory: () => setHistoryOpen(true),
  }

  return (
    <CodeExportContext value={value}>
      {props.children}
      {selection && (
        <CodeExportDialog
          scope={props.scope}
          selection={selection}
          onClose={() => setSelection(null)}
        />
      )}
      {historyOpen && (
        <CodeBatchHistoryDialog
          scope={props.scope}
          onExportBatch={value.exportBatch}
          onClose={() => setHistoryOpen(false)}
        />
      )}
    </CodeExportContext>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export const useCodeExport = () => {
  const context = React.useContext(CodeExportContext)
  if (!context) {
    throw new Error('useCodeExport has to be used within <CodeExportProvider>')
  }
  return context
}
