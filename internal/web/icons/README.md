# icons

Vendored [Lucide](https://lucide.dev) icons rendered as templ components.
SVGs live under `svg/` and are embedded into the binary at build time.

## Use in a template

```templ
import "altalune.id/template/internal/web/icons"

@icons.Icon("check", "size-4 text-blue-500")
```

- First arg: icon name (matches the file basename in `svg/`, without `.svg`).
- Second arg: CSS classes applied to the outer `<svg>`.
- Missing icons render as `<span data-missing-icon="name">` so gaps are
  visible in dev.

Browse available names with `icons.Names()`; check one with
`icons.Has(name)`.

## Add an icon

```bash
make icons-add NAME=trash-2
```

Downloads `trash-2.svg` from the Lucide main branch into `svg/` and it
becomes available on next `templ generate` + build.

## Refresh vendored icons

```bash
make icons-sync
```

Re-fetches every `*.svg` currently in `svg/` from Lucide main. Run this
periodically to pick up upstream fixes.
