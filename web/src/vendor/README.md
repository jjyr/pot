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
