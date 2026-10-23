# Vendored frontend dependencies

`pele.mjs` is the unmodified `dist/pele.js` from [Pele 1.6.0](https://github.com/obsidianmd/pele),
published in `https://registry.npmjs.org/pele/-/pele-1.6.0.tgz`.
It contains all diagram types and has no runtime dependencies. The `.mjs`
extension allows the same file to be imported by the browser and Node checks
without a frontend package manifest or bundler.

SHA-256: `d1ebc1889f3069d7759fe5db34c4ad4b4823285edbee2b537d0ef3d505a76d8d`.

`pele-LICENSE.txt` is the complete upstream license, including Pele's MIT
license and the ISC notice for its Lucide zoom-control icon. Packaging also
copies it to `FrontendDiagramLicense.txt` for the About notice inventory.

## TanStack Virtual Core

`tanstack-virtual-core.mjs` bundles `@tanstack/virtual-core` 3.19.7 from
`https://registry.npmjs.org/@tanstack/virtual-core/-/virtual-core-3.19.7.tgz`.
The upstream runtime is unmodified; esbuild 0.25.12 combines its ESM modules and
replaces `process.env.NODE_ENV` with `"production"` for standalone browser use.
It has no runtime dependencies or network imports. `tanstack-virtual-core-LICENSE.txt`
is the complete upstream MIT license, also packaged as
`FrontendVirtualizerLicense.txt` for About.

Reproduce from the extracted npm package:

```sh
npm exec --yes --package=esbuild@0.25.12 -- esbuild package/dist/esm/index.js \
  --bundle --format=esm --target=es2022 --minify --legal-comments=none \
  '--define:process.env.NODE_ENV="production"' --outfile=tanstack-virtual-core.mjs
```

Tarball SHA-256: `10aa212b74d5bbc72680d56f49e944ccfc1df6a1837c519de5f4cd5d31a48f4b`.
Bundle SHA-256: `c3d4e6bc13802297b60120f7659679656923d6e6047ae5c48f7fd93d18aa6a65`.
