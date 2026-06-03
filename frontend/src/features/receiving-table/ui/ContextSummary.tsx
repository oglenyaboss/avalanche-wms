interface ContextSummaryProps {
  cargoplaceCode: string
  boxBarcode?: string | null
  productBarcode?: string | null
}

interface ChipProps {
  label: string
  value: string
  tone: 'primary' | 'muted'
}

const toneClassName: Record<ChipProps['tone'], string> = {
  primary: 'border-border bg-secondary text-secondary-foreground',
  muted: 'border-border bg-muted text-muted-foreground',
}

function Chip({ label, value, tone }: ChipProps) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-md border px-3 py-1 text-sm ${toneClassName[tone]}`}
    >
      <span className="text-muted-foreground">{label}</span>
      <span className="font-medium">{value}</span>
    </span>
  )
}

// The breadcrumb of what is currently in hand: cargoplace → box → product.
// Items appear as they are scanned, mirroring the Figma context chips.
export function ContextSummary({
  cargoplaceCode,
  boxBarcode,
  productBarcode,
}: ContextSummaryProps) {
  return (
    <div className="flex flex-wrap gap-2">
      <Chip label="Грузоместо" value={cargoplaceCode} tone="primary" />
      {boxBarcode ? <Chip label="Коробка" value={boxBarcode} tone="primary" /> : null}
      {productBarcode ? (
        <Chip label="Товар" value={productBarcode} tone="muted" />
      ) : null}
    </div>
  )
}
