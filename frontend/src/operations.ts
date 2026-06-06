// Display names for the operation dropdown. The operand fields are labelled
// `a` / `b` to match the backend contract, so no per-operation field labels are
// needed here.

import type { Operation } from './api'

export const OPERATION_LABELS: Readonly<Record<Operation, string>> = {
  add: 'Add',
  subtract: 'Subtract',
  multiply: 'Multiply',
  divide: 'Divide',
  power: 'Power',
  sqrt: 'Square root',
  percentage: 'Percentage',
}
