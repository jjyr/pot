# Bundled model directory

`model-directory.json` is an offline seed generated from
https://models.dev/api.json?type=all on 2026-10-15. It retains provider names,
SDK/base URL hints, documentation links, model names, limits, modalities, tool
support, lifecycle/type flags and per-model route overrides. Matching model prices
were enriched from the same upstream API on 2026-10-30; raw cost objects retain
cache and tier dimensions. Other descriptive fields are omitted. Common SVG marks come from
https://models.dev/logos/{provider}.svg. A failed runtime refresh never modifies
this embedded seed.

Update the seed explicitly with `mise exec -- python scripts/update-model-directory`.
Normal builds and tests never download it. Runtime caching and parameter
semantics are documented in [Model directory](../../../docs/model-directory.md).

Source: https://github.com/anomalyco/models.dev, MIT; the upstream license is
preserved in `models-dev/LICENSE`. Provider names and marks belong to their
respective owners.
