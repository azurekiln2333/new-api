const modelNameCollator = new Intl.Collator('en', {
  numeric: true,
  sensitivity: 'base',
})

type ModelOption = {
  label: string
  value: string
}

/** Display ordering only; callers retain their source and selected-value order. */
export function compareModelOptions(
  left: ModelOption,
  right: ModelOption
): number {
  const nameOrder = modelNameCollator.compare(left.label, right.label)
  if (nameOrder !== 0) return nameOrder
  if (left.value < right.value) return -1
  if (left.value > right.value) return 1
  return 0
}

export function compareModelNames(left: string, right: string): number {
  return compareModelOptions(
    { label: left, value: left },
    { label: right, value: right }
  )
}
