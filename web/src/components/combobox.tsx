// A searchable dropdown. Ported from shadcn/ui's combobox (the component ReUI builds on),
// which wraps Base UI's Combobox: the parts and their layout are the same, the classes
// use this app's theme tokens and the icons are inline. The pattern is ReUI's "combobox
// in a popup": a button shows the choice, the popup holds a search box and the list.
import { Combobox as Primitive } from '@base-ui/react/combobox'
import type { ReactNode } from 'react'
import { useT } from '../lib/i18n'
import { cx } from './ui'

export type ComboOption = {
  value: string
  label: string
  /** Shown muted after the label, e.g. "public". */
  note?: string
}

function ChevronIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16" className="size-4 shrink-0 text-muted" fill="none" stroke="currentColor" strokeWidth="1.5">
      <path d="M4 6l4 4 4-4" />
    </svg>
  )
}

function CheckIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16" className="size-4" fill="none" stroke="currentColor" strokeWidth="1.5">
      <path d="M3.5 8.5l3 3 6-7" />
    </svg>
  )
}

type ComboFieldProps = {
  label: string
  name: string
  options: ComboOption[]
  /** The chosen option's value; one that matches no option shows the placeholder. */
  value: string
  onChange: (value: string) => void
  placeholder?: string
  searchPlaceholder?: string
  /** Shown when the search matches nothing. */
  empty?: string
  hint?: ReactNode
  required?: boolean
  disabled?: boolean
}

/** A labelled, searchable dropdown that looks like Field. */
export function ComboField(props: ComboFieldProps) {
  const t = useT()
  const selected = props.options.find((o) => o.value === props.value) ?? null
  return (
    <div className="space-y-1.5">
      <label htmlFor={props.name} className="block text-xs text-muted">
        {props.label}
      </label>
      <Primitive.Root
        items={props.options}
        value={selected}
        onValueChange={(o: ComboOption | null) => o && props.onChange(o.value)}
        itemToStringLabel={(o: ComboOption) => o.label}
        itemToStringValue={(o: ComboOption) => o.value}
        isItemEqualToValue={(a: ComboOption, b: ComboOption) => a.value === b.value}
        name={props.name}
        required={props.required}
        disabled={props.disabled}
        autoHighlight
      >
        <Primitive.Trigger
          id={props.name}
          className={cx(
            'flex h-9 w-full items-center justify-between gap-2 border border-line bg-bg px-3 text-start text-sm text-fg',
            'focus:border-accent focus:outline-none data-[popup-open]:border-accent',
            'disabled:cursor-not-allowed disabled:opacity-60',
          )}
        >
          <span className={cx('min-w-0 truncate', !selected && 'text-muted/60')}>
            {selected ? selected.label : (props.placeholder ?? t.common.choose)}
          </span>
          <ChevronIcon />
        </Primitive.Trigger>
        <Primitive.Portal>
          <Primitive.Positioner side="bottom" align="start" sideOffset={4} className="isolate z-50">
            <Primitive.Popup
              className={cx(
                'group/combobox flex max-h-[min(22rem,var(--available-height))] w-(--anchor-width) max-w-(--available-width) flex-col',
                'border border-line-strong bg-surface text-fg shadow-lg shadow-black/30 outline-none',
                'origin-(--transform-origin) transition-[opacity,scale] duration-100',
                'data-[starting-style]:scale-95 data-[starting-style]:opacity-0 data-[ending-style]:scale-95 data-[ending-style]:opacity-0',
              )}
            >
              <Primitive.Input
                placeholder={props.searchPlaceholder ?? t.common.search}
                className="h-9 w-full shrink-0 border-b border-line bg-bg px-3 text-sm text-fg placeholder:text-muted/60 focus:outline-none"
              />
              <Primitive.Empty className="px-3 py-2 text-xs text-muted empty:hidden">{props.empty ?? t.common.nothingMatches}</Primitive.Empty>
              <Primitive.List className="min-h-0 flex-1 scroll-py-1 overflow-y-auto overscroll-contain py-1 data-[empty]:hidden">
                {(o: ComboOption) => (
                  <Primitive.Item
                    key={o.value}
                    value={o}
                    className={cx(
                      'relative flex w-full cursor-default items-center gap-2 py-1.5 ps-3 pe-8 text-sm outline-none select-none',
                      'data-[highlighted]:bg-raised data-[selected]:text-accent',
                    )}
                  >
                    <span className="min-w-0 truncate">{o.label}</span>
                    {o.note && <span className="shrink-0 text-xs text-muted">{o.note}</span>}
                    <Primitive.ItemIndicator className="pointer-events-none absolute end-2 flex size-4 items-center justify-center text-accent">
                      <CheckIcon />
                    </Primitive.ItemIndicator>
                  </Primitive.Item>
                )}
              </Primitive.List>
            </Primitive.Popup>
          </Primitive.Positioner>
        </Primitive.Portal>
      </Primitive.Root>
      {props.hint && <p className="text-xs text-muted">{props.hint}</p>}
    </div>
  )
}
