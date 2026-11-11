import { Combobox as Primitive } from '@base-ui/react/combobox'
import { languages, setLang, useLang, useT } from '../lib/i18n'

type Language = (typeof languages)[number]

function GlobeIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-4" fill="none" stroke="currentColor" strokeWidth="1.25" aria-hidden>
      <circle cx="8" cy="8" r="6.25" />
      <path d="M1.75 8h12.5M8 1.75c1.7 1.8 2.6 3.9 2.6 6.25s-.9 4.45-2.6 6.25C6.3 12.45 5.4 10.35 5.4 8S6.3 3.55 8 1.75Z" />
    </svg>
  )
}

/** The globe button of the navbar: a searchable list of the interface languages. */
export function LanguagePicker() {
  const lang = useLang()
  const t = useT()
  const selected = languages.find((l) => l.code === lang) ?? languages[0]
  return (
    <Primitive.Root
      items={languages}
      value={selected}
      onValueChange={(l: Language | null) => l && setLang(l.code)}
      // The search matches a language by its own name or its English one.
      itemToStringLabel={(l: Language) => `${l.name} ${l.english}`}
      itemToStringValue={(l: Language) => l.code}
      isItemEqualToValue={(a: Language, b: Language) => a.code === b.code}
      autoHighlight
    >
      <Primitive.Trigger
        aria-label={t.shell.chooseLanguage}
        title={t.shell.language}
        className="flex size-8 items-center justify-center border border-transparent text-muted hover:border-line hover:text-fg data-[popup-open]:border-line data-[popup-open]:text-fg"
      >
        <GlobeIcon />
      </Primitive.Trigger>
      <Primitive.Portal>
        <Primitive.Positioner side="bottom" align="end" sideOffset={8} className="isolate z-50">
          <Primitive.Popup className="flex w-56 max-w-(--available-width) origin-(--transform-origin) flex-col border border-line-strong bg-surface text-fg shadow-lg shadow-black/30 transition-[opacity,scale] duration-100 outline-none data-[ending-style]:scale-95 data-[ending-style]:opacity-0 data-[starting-style]:scale-95 data-[starting-style]:opacity-0">
            <Primitive.Input
              placeholder={t.shell.searchLanguages}
              className="h-9 w-full shrink-0 border-b border-line bg-bg px-3 text-sm text-fg placeholder:text-muted/60 focus:outline-none"
            />
            <Primitive.Empty className="px-3 py-2 text-xs text-muted empty:hidden">{t.shell.noLanguage}</Primitive.Empty>
            <Primitive.List className="py-1 data-[empty]:hidden">
              {(l: Language) => (
                <Primitive.Item
                  key={l.code}
                  value={l}
                  className="relative flex w-full cursor-default items-center gap-2 py-1.5 ps-3 pe-8 text-sm outline-none select-none data-[highlighted]:bg-raised data-[selected]:text-accent"
                >
                  <span lang={l.code}>{l.name}</span>
                  {l.name !== l.english && <span className="text-xs text-muted">{l.english}</span>}
                  <Primitive.ItemIndicator className="pointer-events-none absolute end-2 flex size-4 items-center justify-center text-accent">
                    <svg aria-hidden="true" viewBox="0 0 16 16" className="size-4" fill="none" stroke="currentColor" strokeWidth="1.5">
                      <path d="M3.5 8.5l3 3 6-7" />
                    </svg>
                  </Primitive.ItemIndicator>
                </Primitive.Item>
              )}
            </Primitive.List>
          </Primitive.Popup>
        </Primitive.Positioner>
      </Primitive.Portal>
    </Primitive.Root>
  )
}
