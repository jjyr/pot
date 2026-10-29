# Stage selection artwork

`pot-commander.png` and `agent-roster.png` were generated with the built-in
imagegen tool for the Pot website. The first uses the project's `pot.png` as an
identity reference. The second is a four-column, two-row sprite sheet, displayed
through CSS background positions without modifying the original file.

The robot portraits are illustrative representations of Agent and model choices,
not official brand characters, endorsements, live sessions or additional Agent
adapters. The tile labels distinguish Codex and Antigravity from model families.
Official adapter marks remain separate, unmodified images in each tile label.
The center character depicts Pot. Typography, frames, labels and interactive
selection behavior are HTML/CSS, not baked into the artwork.

## Commander prompt

> Use case: stylized-concept. Asset type: square pixel-art boss portrait for the CENTER TILE of a classic Mega Man 2 / Mega Man 3 NES stage selection UI. Use the supplied Pot mascot as identity reference: a mustard-gold anthropomorphic cooking pot, diagonal oval open rim, small black eye, curved mischievous friendly smile. Create an original dramatic CLOSE-UP HEAD AND SHOULDERS portrait of this SAME POT mascot, huge face filling almost all the square, looking slightly toward the viewer, recognizable sloping pot rim. Keep it a pot, do NOT turn it into a human or robot. Strict low-resolution pixel art, appearance of a 96x96 sprite enlarged with nearest neighbor, obvious crisp stair-step contours, limited 10-color NES palette, no antialias, no smooth gradients. Gold, burnt orange shadow, pale cream highlights, dark navy outline. Dark midnight blue solid background #080d27. Exaggerated confident mastermind grin and arched brow, playful rather than evil. Warm face strongly contrasts dark background. Single portrait, edge-to-edge square composition. NO text, NO lettering, NO UI, NO borders, NO frame, NO logos other than the mascot identity. Not full body; no feet. This is just the central character artwork, frame and selection UI are built separately in HTML.

## Assistant sprite sheet prompt

> Asset type: a precisely aligned SPRITE SHEET for a retro game character selection interface, 4 columns by 2 rows, exactly EIGHT portraits. Landscape 2:1 aspect ratio, 2048x1024 if possible. No spacing or gutters. Each of the 8 square cells is exactly 1/4 image width and 1/2 image height, all use plain deep navy #080d27 backgrounds. Original 8-bit / 16-bit robot boss HEAD portraits reminiscent of the Mega Man 2 / Mega Man 3 STAGE SELECT portraits. NOT a modern 3D rendering. Crisp chunky pixel steps, strong black outlines, 10 to 14 colors per character, bold highlights, close-up faces fill each cell. Each robot has a distinctive helmet shape and expressive face. Top row left to right: cobalt blue engineering robot with angular visor; orange robot with broad triangular helmet and monocle; emerald green scholar robot with square spectacles and dome head; magenta robot with antennae and narrow goggles. Bottom row left to right: cyan robot with twin side antennas and round eyes; red robot with wing-like helmet and determined eyes; violet robot with a helmet shaped like angular pages and friendly mouth; teal robot with a radar dish on helmet. These will be illustrative portraits for specialist assistants, so friendly capable game rivals, NOT scary. Uniform portrait scale, head and shoulders only. NO typography, NO lettering, NO logos, NO borders, NO decorative UI, NO frames. Grid alignment exact edge-to-edge 4x2, no extra cells. The web page supplies frames and names separately.

## Pixel font

Press Start 2P by CodeMan38, distributed under the SIL Open Font License 1.1.
Source: https://fonts.google.com/specimen/Press+Start+2P
The unmodified font and license are bundled locally; visiting the website does not
request Google Fonts or send visitor data to a font CDN. The fixed English
`SELECT YOUR ALLY` label is registered in every locale catalog; surrounding content
and controls are localized.

## Directional Pot sprite sheet

`pot-directions.png` was generated with the built-in imagegen tool, using
`pot-commander.png` as the identity reference. CSS selects one of nine poses
and mirrors the upper-left pose for the intended gaze. Slight vertical crop
adjustments prevent neighboring rows from showing through. The source file is
preserved unmodified. The center pose is available as a static fallback.

Generation prompt:

> Create a production game sprite sheet by editing the referenced golden Pot mascot portrait. EXACT square image with 3 columns and 3 rows, nine equal square cells, NO GUTTERS, NO borders, NO labels, NO text. All backgrounds solid dark midnight navy #080d27. Same original golden anthropomorphic cooking pot mascot in every cell, open slanted pot rim, happy confident face, huge closeup face and shoulders. Crisp authentic 16-bit pixel art, chunky clearly visible pixels, restrained palette, NO smooth gradients. Consistent character scale and head center in each cell, all outlines fully inside each cell with 8% padding. These are nine different head and EYE orientations for a Mega Man style stage select: top left cell head turned and looking UP LEFT; top middle looking UP with face tipped up; top right looking UP RIGHT; middle left looking LEFT strongly, sideward three-quarter face; center looking straight AT VIEWER; middle right looking RIGHT strongly; bottom left looking DOWN LEFT; bottom middle looking DOWN with face tipped down; bottom right looking DOWN RIGHT. Use two readable dark eyes with cream sclera and dark pupils visibly aiming toward each specified direction, keep cheerful smile. Head turn, facial position and rim perspective MUST change with the direction, not just moving the whole image. Character identity and gold colors remain consistent. Output a single exact 3x3 sprite atlas ready to slice by thirds, 1536x1536 if possible.
