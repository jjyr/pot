# Provider brand artwork

These offline SVGs take precedence over models.dev's monochrome marks in the
shared model directory, so the picker, configured connections and editor use
the same brand colors even after a catalog refresh. Brands without an override
continue to use the directory artwork.

Source: [Lobe Icons](https://github.com/lobehub/lobe-icons), revision
`8a1b2821bb768c484fe8da20c5e1838b6bf551a9`,
`packages/static-svg/icons/`. Google, DeepSeek, Mistral, Cerebras and DeepInfra
use their `*-color.svg` variants; Together AI uses `together-color.svg` and
Zhipu AI uses `zhipu-color.svg`. Grok (`xai`) uses `xai.svg` from the official
Grok brand mark (`https://grok.com/images/favicon.svg`) with standalone SVG paths.

The original MIT license is retained in `LICENSE` and included in the packaged
`ModelDirectoryLicense.txt`. Provider names and marks belong to their owners.
