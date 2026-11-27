# shotdb rewrite

Static page that shows the ground tech trees of all nations side by side and colours each
vehicle card by how well a chosen shooter does against it in realistic battles (RB). The shot
simulation is written in Go, built to WebAssembly and runs in Web Workers in the page: one
manager worker reads the game files and runs the queue, and a pool of compute workers fires
the shots.

**This project was fully vibecoded.** An AI wrote all of the code, and the results are not verified against the real game.

## Run

You need two things:

1. The War Thunder game files: the install folder, or an unpacked datamine tree (see below).
2. Go 1.25.6 or newer. The build needs no Node.js and no npm.

The trees, names and battle ratings come from the game files. Only the unit pictures are
loaded from the wiki's static content, which the game files do not give in a format the
page can read. The build never fetches game data. The build uses the network only for the
Go modules, and after this step all is offline:

    go mod download
    ./build.sh                    # builds everything into dist/
    ./serve.sh                    # serves dist/ at https://localhost:8000/  (HOST, PORT to change)

`build.sh` deletes `dist/`, builds `dist/manager.wasm` and `dist/compute.wasm`, copies `wasm_exec.js` from the local
Go (`$(go env GOROOT)/lib/wasm/`) and then runs `go run ./cmd/build`. `cmd/build` uses the
Go API of esbuild (github.com/evanw/esbuild) to bundle and minify `web/src/app.ts` to
`dist/app.js`, `web/src/manager.ts` to `dist/manager.js`, `web/src/compute.ts` to
`dist/compute.js` and `web/style.css`, and it writes
`index.html` with the script path for `app.js`. `-minify=false` keeps the bundles readable.
All paths in `dist/` are relative, so `dist/` works under any subpath.

esbuild only removes the TypeScript types. No build step checks the types. The `tsconfig`
files are there for the editor only.

`cmd/serve` serves `dist/` over HTTPS with a self-signed certificate that it makes at each start.
The browser shows a warning, which you must accept. Use `-dir` to serve another folder. Any
other static server also works. The GitHub Pages workflow runs `build.sh` and publishes
`dist/`.
The game files never go through the server: the page reads them in the browser, from the
folder that you pick.

Open the page, press **Open game folder** in the middle of the page and pick the **War Thunder install folder** (the
one with `aces.vromfs.bin`). The page reads `aces.vromfs.bin`, `char.vromfs.bin`,
`lang.vromfs.bin`, `warthunder.yup` (for the version), the vehicle model packs in
`content/base/res/tanks/*.grp`. Of the packs it reads only the parts it needs.

An **unpacked datamine** tree (as in gszabi99/War-Thunder-Datamine: the folder with
`aces.vromfs.bin_u`, `char.vromfs.bin_u`, and `lang.vromfs.bin_u`) gives the units, trees, armour parts, shells and tables, but it has
no model packs, so there is no geometry and every target
fails with "no content/base/res/tanks here". A datamine tree works only with the packs copied
in at `content/base/res/tanks/`.

### How the page remembers the folder

Chromium browsers use the File System Access API. The page reads only the files above, and
it keeps the folder handle in IndexedDB. On the next visit the page asks the browser for read
access to that folder:

- If the browser gives access, the page opens the folder at once.
- If the browser wants a new consent, the page shows **Reopen <folder>**. Press it to give
  read access again. The browser requires a click for this step.
- If the handle is gone or invalid, the page drops it and shows **Open game folder**.

**Choose another** picks a different folder. **Forget** removes the remembered folder.

Firefox has no `showDirectoryPicker`. There, press **Open game folder** and pick the install
folder, or drag the install folder from the file manager onto the page. The button opens a
folder input (`<input type="file" webkitdirectory>`). The page makes this input only in
browsers that have no `showDirectoryPicker`, so Chromium browsers never use it. Firefox calls
the pick an upload: its dialog has the title "Select Folder to Upload" and the button
"Upload", and then it asks "Are you sure you want to upload all files from …?". Nothing
leaves the computer: the page reads the files in the browser and sends them to no server.
Cancel in either dialog does nothing. From the files of the folder, the page keeps only the files above. Without
`showDirectoryPicker` the page cannot keep access to the folder, so open or drop it on each visit.
The start text names Firefox when the browser is Firefox, and says "this browser" in other
browsers without the API, for example Safari. A datamine tree does
not work this way.

In these browsers, a folder without `aces.vromfs.bin` at its top gives an error, and the data
on the page stays.

Before the first good open, and while no open has given data, the folder controls stand in the
middle of the page: a short text on which folder to pick, the buttons above, and a status line.
The status line shows only news: a load, an error, the access prompt or a failure to remember the
folder. Once the page shows game data, the controls go into the **Game folder** part of
the settings (see [Settings](#settings)). There the page also shows the folder, the source, the
version and the number of vehicles. In Firefox the button is then **Open another folder**, with a
note on drag-and-drop.
Drag-and-drop works at all times. When an open fails and the old data stays, the error shows in
a line under the header, and the cog gets a red mark. **Hide** removes the line and the mark.
When you pick or drop a folder while the page still reads an older one, the page opens only the
newest folder.

Then pick the shooter in the box at the top left, or click a card. The page does not pick a shooter
by itself, so no shots run until you pick one. Shots run from the shooter's RB battle rating
outwards, up and down. Hover a card for the details.

**Pause** next to the progress bar, under the shooter box, stops the queue: the compute workers finish the targets that they hold
and take no new one. **Resume** continues from the same place. A new shooter or round starts
its queue at once, also when the queue is paused.

A click on a card or a round, or a change of **Shots**, first asks in a small bar over the
trees when it would stop a queue that has work left (running or paused). When the settings are
open, the bar stands in the settings under **Shots**: **Run** (or Enter)
starts the new queue, **Cancel** (or Escape) keeps the old one and puts **Shots** back. The
results of the stopped queue stay kept (see [Kept results](#kept-results)). The page does not
ask when no queue runs, when the queue is done or stopped, when the kept results hold all
targets of the new choice, or when you pick the shooter in the box at the top. After a change
of shooter, **back to <shooter>** under the box goes back to the shooter before.

### Kept results

The page keeps each shot result in IndexedDB (database `shotdb`, stores `results` and
`runs`). When you go back to a shooter, round or **Shots** count of before, the page shows
the kept results at once and runs only the other targets. This is also true after a reload
and for a queue that stopped before its end. The progress text shows how many results are
kept, and the time a pair counts only the targets that ran now.

The key of a run holds all that changes a result: the build of the simulation code (a hash
of `manager.wasm` and `compute.wasm` that `cmd/build` puts in the page as `BUILD_ID`), the
source and the version of the game data, the shooter, the round (-1 for the best of all) and
the shot lines a round. A result stays valid for 24 hours after it ran. The page does not use
an older result and runs the target again. Errors are not kept.

When all rounds are fired, a result also holds the views, the score and the shots of each
round (`each`). The same round fired alone gives the same values (the random seed is per
round, target and line), so a forced round also takes its results from the run of all rounds
with the same key. The page never makes a forced result from a run that does not hold that
round. Only the store keeps `each`.

Before each queue, the page deletes the runs whose newest result is older than 24 hours.
Then, while there are more than 40 runs or they take more than about 100 MB, it deletes the
run that it used least recently. A full queue takes about 1.2 MB with one round, and up to
about 11 MB with 11 rounds. These sizes are from before the follow-up shots, which make the
results 12 to 33 % larger (see "Follow-up shots").

The header has two parts. At the left are the brand, the cog, the shooter box, the shooter's
role and BR, and the progress bar. At the right is the panel of rounds. The panel shows the
shooter's guns and each round that the simulation fires: type, calibre (mm), mass (kg), muzzle
speed (m/s), flat penetration at 500 m (mm) and filler (g and explosive). Each gun's rounds are
in tables side by side. No text is cut and no round is hidden: names wrap, and the panel has no
scroll bar. For each shooter and window width, the page tries tables of 3 rows up to the most
rounds of one gun, with the panel at the right and with the panel under the shooter box. It
keeps the layout that gives the lowest header. Below a window width of 1000 px the panel is
always under the shooter box. A long shooter name makes the box wider. The **best** column
counts the targets on which each round gave the best score (also on kept results). When you hover a card, the panel
marks the round that the card shows. Click a round to fire only that round. Click it again to
fire the best of all rounds.

### Views of the trees

The line under the header holds the view of the trees and the nation chips. The view has four
modes: **Vertical**, **Horizontal**, **Compact** and **One nation**. The chip of the shooter's
nation has a yellow ring in each mode. The page keeps the mode and the nation in `localStorage`.
The name of **Vertical** was **All trees** before. A kept mode of `all` gives **Vertical**.

**Vertical** shows the shown nations side by side, with the low ranks at the top. A nation chip
shows or hides that nation.

**Horizontal** turns each nation tree on its side, as the game tree. The nations are bands, one
under the other. In a band, each research line is a row, and the premium rows follow under a
dashed line. The ranks go up from left to right, and a line separates each two ranks. The wheel
scrolls down through the nations. Shift and the wheel, the scrollbar at the bottom of the window,
or a drag with the left mouse button scroll sideways. A drag that starts on a card does not shoot
with that card. A rank ruler stays at the top, and the nation names stay at the left. A nation
chip shows or hides that nation.

All bands use the same rank columns. A rank column is as wide as the widest nation needs at that
rank, so the ranks line up in all bands. In a rank, the rows of the game tree are the x order.
The cards of a folder stand side by side. An x place is as wide as the largest folder at that
place in the nation. With the game data of 2026-11 (10 nations, 1254 cards, 8 ranks), the page
is 6040 px wide and 3194 px high. A rank column is 572 to 860 px wide.

When you pick a shooter, and when you change to this mode, the view scrolls sideways to the rank
column of the shooter. The ruler marks that rank in yellow. When the column is wider than the
view, the shooter's card goes to the middle of the view, but the view stays in the column. When
the shooter's nation is hidden, the start of the column comes to the left. The vertical position
does not change. The view does not scroll by itself again until the next shooter, so a new
result does not move it. With no shooter, the view starts at the left. With
`prefers-reduced-motion: reduce` the view jumps with no motion.

**Compact** is the **Vertical** view with each vehicle as a small square tile: the centre of its picture
in the result colour, and the front, side and rear bars in the bottom 4 px. A nation chip shows
or hides that nation. The page sets the tile size so that all shown nations fit the window
width, at most 70 px (the picture height and the bars). The tile size shows at the end of the
line. The trees scroll down. The picture keeps its natural size up to 1.25 times the tile
width. For a smaller tile the page scales the picture down to 1.25 times the tile width, so a
tile always shows at least 80 % of the picture width. Below 8 px the page keeps 8 px tiles and
scrolls sideways. Before this mode, Compact was a toggle of its own. A kept Compact toggle with
the view of all trees gives the Compact mode.

**One nation** shows the tree of one nation, as in **Vertical**, in the middle of the window.
A change to this mode shows the first nation. A nation chip shows its nation. The free space at
the left and at the right of the tree shows an arrow and the name of the previous or the next
nation. Click it to change the nation. The wheel over this space scrolls the tree. When this
space is less than 56 px wide, two small arrow buttons after the mode selector do the same. The left and right arrow keys also change the nation. The arrow keys do nothing while the
settings are open, while the page asks a question, or while a text box or a list has the focus.
The new tree slides in from the side of its chip or its arrow. With
`prefers-reduced-motion: reduce` the tree changes with no motion.

### Settings

The cog to the left of the shooter box opens the settings. Escape, **Close** or a click outside
the settings closes them. The settings hold **Shots**, **Workers**, the colours of the results and
the game folder. The page keeps each setting as below.

**Shots** sets the number of shot lines that each round fires at
each target: 40, 80, 160, 384 or 768. The default is 160. More lines give a finer result and
take longer (see [What the simulation does](#what-the-simulation-does) and [Speed](#speed)).
The page keeps the choice in `localStorage`. A change starts the queue of the shooter again
with the new count, also when the queue is paused. The results of the old count stay in the
store (see [Kept results](#kept-results)). The forced round stays. A new shooter or a new open
keeps the count.

**Workers** sets the number of compute workers (Web Workers) that
run the shots. One more worker, the manager, reads the game files and runs the queue (see
[Workers](#workers)). The default is half of the CPU threads, at most 8. The page keeps the
choice in `localStorage`. A change takes effect at once. A removed worker stops at once, and
the manager puts its targets back at the front of the queue.

When a compute worker fails in a shot, that target shows the error. The other targets of the
worker go back in the queue, and a new worker starts. After 3 failed workers in a row with no
good result between, no new worker starts. When no worker is left, the progress bar shows that
the queue stopped. A change of **Workers** starts new ones.

When the manager fails, the page shows the error and the queue stops. The next open starts a
new manager. When an open fails, the manager keeps the data that it had, and its queue goes on.

URL parameters: `view` (`horizontal`, `compact` or `nation`; no `view` or `all` is **Vertical**) and `nation` (the
nation of the one-nation view; a link or a reload keeps it), `nations` (comma list), `speed` (targets that a compute worker holds at once:
one runs and the others wait; 1 to 8, default 2; the manager uses 2 for other values) and
`hover` (unit id to pin a tooltip once the queue is done; the one-nation view changes to the
nation of that unit).

### Command line

    go run ./cmd/shotsim -data <install or datamine> us_m4a2_sherman germ_pzkpfw_vi_ausf_h1_tiger

prints the result JSON that the page shows, with the result of each round in `each`. `-lines` sets the shot lines a round, as **Shots**
does in the page (default 160).

## Layout

- The `.vromfs.bin` containers and the binary BLK files are read with `wtcontent` and `wrpl`
  of github.com/maxsupermanhd/wrpl-inspector. `game/data.go` gives the BLK reader each
  container's name map and a zstd decoder with the container's dictionary, and turns int
  values into float64, as in the datamine's `.blkx` JSON.
- `grp/` reads GRP2 resource packs and the collision resources in them (the compressed
  "v3" format with a quantised tree per node, and the older plain one).
- `geom/` holds the collision nodes and builds a BVH that traces rays through them.
- `dm/` holds the data that the simulation reads: vehicles, damage parts, crew, racks, shells
  and the global tables. It has no loaders.
- `game/` loads units (RB BR and rank from `wpcost.blk`, role from `unittags.blk`, names
  from `units.csv`), damage parts, crew, ammo racks, shells, the damage model tables and the
  collision nodes, from either source, into the `dm` types. `game/shop.go` lays out the trees
  (see below).
- `sim/` is the simulation: `sim.Rounds(tables, shooter)` and
  `sim.Simulate(tables, shooter, target, nodes, round)`. It imports only `dm` and `geom`.
- `packet/` encodes what the manager sends to the compute workers (see [Workers](#workers)).
- `cmd/manager` is the manager's wasm. Its doc comment lists the messages of the page.
- `cmd/compute` is the compute workers' wasm: `compute.tables(packet)`,
  `compute.shooter(packet, round)` and `compute.simulate(packet, collision)`. `round` -1 fires
  every round and keeps the best. It imports no `game`, BLK or vromfs code. It uses `grp` and
  zstd to parse the collision resource of each target.
- `web/` holds the page sources: `index.html`, `style.css` and `src/`. `src/manager.ts` runs
  the manager's wasm and reads the chosen folder. `src/compute.ts` runs the compute wasm.
  `src/app.ts` is the page, the pool of compute workers, the remembered folder, and the
  dropped or picked folder in browsers without the File System Access API. It draws
  and passes the user's actions to the manager. `src/store.ts` keeps the remembered folder
  and the kept results in IndexedDB. `src/types.ts` has the shapes of the JSON and
  of the worker messages. `src/wasm.d.ts` and `src/fs-access.d.ts` declare the wasm APIs and
  the parts of the File System Access API that the TypeScript DOM types do not have.
- `cmd/build` bundles `web/` into `dist/` with esbuild.
- `cmd/serve` serves `dist/` as static files over HTTPS.

## Trees and icons

The trees come from `char.vromfs.bin/config/shop.blk`. Each `range` block under
`country_<nation>/army` is one column. The units of a rank fill the rows of their column in
file order. A group block (for example `us_sherman_group`) and a unit with its `slaveUnit`
are one stack of cards. A range where more than half of the units are bought with gold
(`costGold` in `wpcost.blk`), gifts, event prizes, market items or squadron vehicles
(`isClanVehicle`) goes to the premium side. The decoded BLK is a map, so `game` reads the file
order from the BLK block table (or the key order of the `.blkx` JSON). The nation and role
names come from `lang/_common_languages.csv` and `lang/menu.csv`.

The card pictures are `https://static.encyclopedia.warthunder.com/slots/<id>.png`, set as the
image source in the page. The game's own slot icons are DXT5 textures packed with Oodle in
`ui/atlases.vromfs.bin`, which no pure Go code reads. A card without a picture shows no image.
Icons load when a card comes near the view.

Compared with the wiki, the trees show 15 more vehicles: event copies (`*_event`, `*_race`),
the football tanks and a few other vehicles with `showOnlyWhenBought`. The game files have
no flag that tells these from the other `showOnlyWhenBought` vehicles that the wiki shows.

## What the simulation does

Each target is shot on its real collision model at 500 m. For each view the shooter stands
at several bearings from the target's nose, at several elevations (degrees down). Each
bearing and elevation is one shot direction. Each direction lays a 28-cell grid over the
silhouette and keeps n of the cells that hit. **Shots** sets the bearings, the elevations
and n:

| shots | front bearings | side bearings | rear bearings | elevations | n | front, side, rear lines |
| --- | --- | --- | --- | --- | --- | --- |
| 40 | -20, 0, 20 | ±80, ±100 | 160, 180, 200 | 3 | 4 | 12, 16, 12 |
| 80 | -20, 0, 20 | ±80, ±100 | 160, 180, 200 | 0, 6 | 4 | 24, 32, 24 |
| 160 | -20, 0, 20 | ±80, ±100 | 160, 180, 200 | 0, 6 | 8 | 48, 64, 48 |
| 384 | -20 to 20, every 10 | ±80, ±90, ±100 | 160 to 200, every 10 | 0, 3, 6 | 8 | 120, 144, 120 |
| 768 | -20 to 20, every 10 | ±80, ±90, ±100 | 160 to 200, every 10 | 0, 3, 6 | 16 | 240, 288, 240 |

The front and the rear get 30 to 31 % of the lines at each step, the side 38 to 40 %. The
grid cells that hit are put in column order and cut into n equal runs. The direction keeps the
middle cell of each run, so the lines spread across the width of the silhouette. A larger
n puts the lines closer together. 384 and 768 also add the bearings between the old ones and
an elevation of 3 degrees. A direction that hits the vehicle in fewer than n cells keeps all of
them. On the 2.61.0.42 install no target has such a direction, also at 768. Every round of the shooter is fired along every
line and the round with the best score is shown. The random parts (dispersion, ricochet
rolls, fragments, damage effects) use a fixed seed per round, target and line, so a result is
the same every run.

From the game, as the game does it (formulas from the knowledge bank):

- **Geometry.** The collision nodes of the model (`<part>_dm`), with their triangles and
  flags. A ray gives every part it crosses, with the entry face's real angle. Nodes that a
  stock vehicle does not have (`hideNodes` of modifications) are left out.
- **Armour per part.** `armorThickness` and the class from `DamageParts`, its groups and
  `armor_classes.blk`: quality per mechanism, thickness caps, `armorThrough`, hp, damage
  multipliers, ricochet and spall flags. Plates are thin sheets in the mesh and are charged
  their nominal thickness at the hit angle. Parts flagged solid in the mesh (cast turrets,
  mantlets, composite blocks, engines, breeches) are charged by the chord the ray cuts.
- **Kinetic penetration.** Drag over range, Lanz-Odermatt for long rods, De Marre (with the
  APHE filler modifier) for full calibre rounds, the `armorpower` table for the others. No test
  in this repository checks these against the stat cards. Projectile type defaults
  (`projectile_types/<bulletType>.blk`, for example the slope preset of APCBC) are merged
  under each bullet.
- **Slope and ricochet.** `slope_effect.blk` (rows blended on calibre to armour, the breach
  rule, the cap of 20), `ricochet.blk` per hit, the bounce in the mirror direction with
  `ricochetPenetrationModifier` and 0.3 of the speed, up to two bounces. Penetration
  dispersion of ±2.5 % at each plate.
- **Fuse and filler.** The fuse arms on a plate that costs more than `explodeTreshold` and
  fires after `fuseDelayDist`, or on the face of a plate it cannot go through. The burst
  throws the body's fragments (from `explosiveTypeToShattersParams`: count, pen, damage and
  reach by filler share, in the round's own segments for HE and HEAT), splash on modules in
  reach, and overpressure in closed compartments. An APHE body that keeps its integrity
  (the game's integrity curve) flies on after the burst.
- **HEAT and HESH.** The jet's `armorPower` with the game's start spread, 100 mm lost per
  metre of air, spall from the jet's preset. HESH acts along the normal.
- **APDS, APCR core breakup** (`breakingArmorThickness`, `breakingScaleByArmor`) and the
  broken core's loss in air gaps.
- **Spall.** Cones from `secondary_shatter_presets.blk` behind each perforated plate, in
  both the residual-penetration and the mass mode, with the game's gates on thin plates and
  overmatch. A cone starts at the exit face of a solid part and at the entry face of a plain
  plate, whose node can be a closed shell. Each fragment's penetration falls off with
  distance and is charged by each part it crosses, except the part that threw it.
- **Damage and kills.** Each part's hp, its `onHit` rules (the strongest rule below the
  damage, gated by `partDamageEffectThreshold`) and `onKill` draws: fires, ammo explosions,
  fuel tank explosions, crew and module kills. The vehicle dies when fewer crew than
  `minCrewCount` (default 2) live, when an internal fuel tank explodes, or when a rack
  goes off as the game's ammo script says. An `expl` on a shells rack whose round has a
  filler is an explosion, which ends the vehicle if the rack has `fatalExplosion`. A `fire`,
  or an `expl` on a charges rack or on a rack whose round has no filler, is an ammo fire,
  which ends the vehicle if the rack has `fatalFire`.
  An exterior tank (group `fuel_tanks_exterior`, fire preset `fuel_exterior`) does not end
  the vehicle.

Approximated, and why:

- **A fatal ammo fire ends the vehicle at once.** The script sends `EventAmmoStowageDoFatalFire`,
  and the game has the death reason "Ammunition load burnt down", but the native handler of
  the event is not decoded, so its delay is not known. The script's test that the round in
  the slot has powder is not made: every round is taken to have it.
- **An exterior fuel tank's explosion** does nothing. The game gives it its own splash
  table (`externalFuelMassToSplashParams` in `damagemodel.blk`, by fuel mass), but the fuel
  mass of each tank is not read here.

- **One ray per shell and fragment.** The game traces a small pattern of rays per shell and
  groups them; here the centre ray stands for the shell. Fragments are traced as at most 8
  rays per cone band or warhead segment, each carrying its share of the count, to keep a pair
  near a second in wasm.
- **The APHE burst** throws its statistical shatters over a full sphere from the burst point,
  as the bank describes the effect but not the exact cone.
- **Splash** reaches a module when the armour on the straight line from the burst to the
  module's centre is less than the splash penetration. Plates on that line cost their
  line-of-sight thickness (nominal / cos, with the class caps), solid parts their chord, and
  modules on the way cost nothing. The game's own visibility test is not known. The
  splash table of `explosive.blk` gives one penetration per filler mass, so the
  penetration does not fall off with distance; only the damage falls off between the inner
  and the outer radius. Each module in reach takes its own damage message and rolls its own
  effects, as for any other hit. Through thin parts (tracks, a lower front plate, a hull
  roof) a large HE round can still reach the racks or the crew of a modern tank, and the
  overpressure rule then acts on the whole compartment.
- **The vehicle is stock and full**: every rack is full, the turret faces forward, no
  modifications are fitted.
- **No shell yaw, no stabilisation loss, no sabot petals, no movement**, a fixed 500 m.
- **ERA and composites** act only through their armour classes (quality per mechanism) and
  their chord; ERA does not burn out after a hit.
- **Some modern models merge several parts into one collision node** in the v3 format; such
  a node takes the part named by the node.
- **Outcome per line**: kill when the vehicle dies; "pen, core broke" when a module inside
  took damage and the core broke; "pen, no kill" when a module inside took damage;
  "ricochet" when the shell bounced and did nothing inside; else "no pen". Score per view:
  `(kills + 0.4 x (pens + core breaks)) / lines`; card score: front 0.5, side 0.3, rear 0.2.
  The result key of "pen, core broke" is `shatter`. It is a core that perforated a plate and
  then broke up, not the game's "shell shattered" message on a shell that did not penetrate.
  Such a shell counts as "no pen" or "ricochet".

## Follow-up shots

After the first shot on each line, the simulation fires more shots. These shots do not change
the outcomes, the scores, the card colour or the choice of the best round. The gain bar on each
card and the hover and pinned details show their effect (see "Colours"). `shotsim` prints them in `follow` of each view, and the round's schedule in
`followUps`, `followSec` and `thirdSec`.

The shooter's schedule comes from the weapon of the round. Each barrel fires at its
`timeOffset`, then each `1 / shotFreq` (the best loader; a stock loader is 1.3 times slower).
After `bulletsCartridge` shots, a barrel waits for the magazine (`reloadTime`). The magazine
is full at the first shot. The follow-ups are the shots in the 1.5 s after the first shot, and
at least one: the M4A2 fires one at 5 s, the M42 fires 6, the ZSU-23-4 fires 85. A guided
round fires no follow-ups, no pairs and no third shot.

1. **Follow-ups.** A first shot that penetrates ("pen, no kill" or "pen, core broke"), damages crew (hp lost) and
   does not kill passes the gate. The follow-ups go along the same line, each with its own
   seed, on the damage state that the shots before left: hp of each part and the dead parts.
   The armour does not change, and a fire that does not kill does nothing. The chain stops at
   the kill. `follow.lines` counts the gated lines, `kills` the lines that a follow-up kills,
   `shots` and `seconds` the sums of the follow-ups and of the time up to each kill.
2. **Pairs.** In one shot direction, each gated line i takes one shot along each other line j
   of that direction that alone does not kill. The shot on j has its own seed and goes on the
   state after i. The order counts: (i, j) and (j, i) are two pairs. `pairs` and `pairKills`
   count them.
3. **No return fire.** After a first shot that does not kill, the target cannot fire its main
   gun when every mount has a dead barrel (unless `brokenBarrelAllowShoot`) or a dead breech,
   or when a turret drive is dead, or when the gunner is dead and no live crew part has the
   override (`secondGunnerDm`). A drive that lists many parts stops when all of its parts on
   the model are dead. The gun stays silent for good, except for a dead gunner with a live
   substitute: then for the seat change time, `gunnerChangeTime` x `changeTimeMult` x the best
   skill multiplier (2.4 s). The simulation does not model repairs. `silenced` counts these
   lines, `third` the lines where the shooter's third shot comes before the window ends, and
   `thirdKills` those that the second or third shot kills.

The extra shots cost time. These are native times, with 8 goroutines on the 2.61.0.42 install
and the queue of 1254 targets. The SPAA rows ran on 4 cores at low priority, before and after
the change under the same limit. "JSON" is the size of all results of the queue, as the store
keeps them:

| shooter | lines | wall before | wall after | JSON before | JSON after |
| --- | --- | --- | --- | --- | --- |
| M4A2 | 40 | 33.8 s | 41.5 s | 4.73 MB | 5.55 MB |
| M4A2 | 160 | 47.6 s | 95.5 s | 4.89 MB | 6.15 MB |
| M4A2 | 768 | 127.7 s | 446.5 s | 5.09 MB | 6.76 MB |
| M42 | 40 | 16.3 s | 23.4 s | 2.88 MB | 3.29 MB |
| M42 | 160 | 27.5 s | 65.6 s | 2.96 MB | 3.55 MB |
| M42 | 768 | 61.9 s | 171.3 s | 3.09 MB | 3.86 MB |
| ZSU-23-4 | 160 | 38.3 s | 119.5 s | 3.75 MB | 4.28 MB |
| M163 | 160 | 35.1 s | 76.3 s | 3.75 MB | 4.22 MB |

The pairs cost the most at many lines: a direction with n lines gives up to n(n-1) pairs. The
85 follow-ups of the ZSU-23-4 stop at the kill, so the cost does not grow as much as the
follow-ups. The simulation sets no cap on the follow-ups.

The details show the third shot only for a round with follow-ups. A dead drive stops only the
aim, so a gun that already points at the shooter can still fire. The simulation does not model
this limit.

## Colours

The card colour is the first-shot score s of the card: `hsl(s x 125, 58 %, 30 %)`, from red at 0
to green at 1. The three strips at the bottom are the scores of front, side and rear.

The gain bar over the strips shows the card score on a scale of 0 to 100 %. The white tick is
the first-shot score. The solid run is the gain from the follow-ups. The dotted run is the gain
from the pairs. A guided round fires no extra shots, so its cards have no bar. The settings show
the legend. For each view, with k kills, p pens and core breaks, n lines, g gated lines, f
follow-up kills and r = pair kills / pairs:

- first shot: `(k + 0.4p) / n`, the score as before
- with the follow-ups: `(k + f + 0.4(p - f)) / n`, so each follow-up kill turns a pen into a kill
- with the pairs too: the line above plus `0.6 r (g - f) / n`

A pair starts on a gated line, so r applies only to the gated lines that the follow-ups did not
kill. All three stay in 0 to 1, and each gain is 0 or more: f is at most g, and g is at most p.
The card score of each is front 0.5, side 0.3, rear 0.2, as for the first shot.

The details show a table with one column per view. Its rows are the scores above, the count of
each outcome, the lines, the extra shots and the plates. "Follow-up" is follow-up kills / gated
lines, with the mean shots and time to the kill. "Pair kill" is pair kills / pairs. "Gun out" is
the lines after which the target cannot fire back. Under it are the lines with a third shot in
time and the kills in three shots. Each plate shows the first armour met, the mean thickness in
mm and the shots (x N). The footer gives the three card scores, the shots and the time.

## Workers

The page runs two wasm binaries, each in its own kind of Web Worker.

- **The manager** (`manager.wasm`, one worker) reads all game files: the containers, the BLK
  files, the language files, the trees, the tank packs, and it loads the vehicles. It also
  runs the queue: the order outwards from the shooter's BR, the forced round, the pause, the
  changes of shooter, round and line count, and a queue number that drops the answers of an old queue. It
  gives the targets to the compute workers and collects the results. The page owns the choice
  of shooter, round and line count and sends it in one message. Each new queue waits until
  the page gives the targets that it has kept results for; the manager counts them as done
  and does not run them.
- **The compute workers** (`compute.wasm`) read one part of one file per target, and no other
  file. Each one gets the global tables once per open (28 KB), the shooter and the line count
  once per queue, and
  then one packet per target: the target's damage parts, crew and racks (68 KB on average,
  153 KB at most). With the packet comes a Blob of the target's collision resource in its
  tank pack. The compute worker starts to read the Blob when the target arrives, and it parses
  the collision nodes itself. The manager keeps two targets in each worker: one runs and the
  next waits, so that a worker does not wait for the manager.

The manager sends each packet as a transferable ArrayBuffer over a MessageChannel straight
to the compute worker. The page only makes the channels. A packet takes one copy out of Go,
no copy on the way and one copy into Go. A Blob is only a reference to a part of the file, so
the manager does not read the collision resource. Package `packet` encodes the packets with
encoding/gob, which keeps every float64 exact, also +Inf.

The manager does the work that does not depend on the shooter once per open: the game data,
the packet of the tables and the list of targets. It keeps the last 32 loaded units, the
rounds of each weapon file that it read, and the place of each collision resource that it
found. It keeps no target packets for the next queue: all packets of a queue take 86 MB, and
such a cache stays in wasm memory, which never shrinks.

## Speed

Measured on the 2.61.0.37 install:

- Native, every one of the 1263 ground units as target, no target failed: with the M4A2
  (four rounds) 132 ms a pair on average, 174 ms at the 90th percentile, 279 ms at most;
  with the Leopard 2A6 (four rounds) 225 ms, 308 ms and 636 ms.
- In the page (wasm in a worker, headless Chromium), including reading and parsing each
  target's collision resource: the whole M4A2 queue of 1239 targets took 469 ms a pair on
  average. Modern pairs are the slowest: Leopard 2A6 on modern targets about 1.0 s, M1A2 on
  T-90A 1.1 s; WWII pairs 0.43 to 0.56 s. Wasm runs about 3.4 times slower than native.

The cost is mostly the shot lines (160 a round at the default **Shots**) and the fragment rays
behind each perforation. A compute worker builds the mesh, the BVH and the lines of each target, and
keeps them only while it fires at it. A queue fires at each target once.

In the page with the manager and the compute workers, on the 2.61.0.42 install (headless
Chromium, a Ryzen 7 3700X with 8 cores and 16 threads, the queue of 1254 targets). The time
of a pair counts the decode of the packet and the parse of the collision resource in the
compute worker, and not the load of the target in the manager. "Idle" is the share of the
time that a compute worker waits for a target.

| shooter | compute workers | wall time | ms a pair | idle | manager busy |
| --- | --- | --- | --- | --- | --- |
| M4A2 | 4 | 157 s | 498 ms | 0 % | 9 % |
| M4A2 | 8 | 83 s | 523 ms | 0 % | 22 % |
| M4A2 | 16 | 70 s | 872 ms | 0 % | 46 % |
| BT-5 | 4 | 118 s | 372 ms | 0 % | 12 % |
| BT-5 | 8 | 63 s | 398 ms | 0 % | 27 % |
| BT-5 | 16 | 52 s | 653 ms | 0 % | 59 % |

For each **Shots** step, the M4A2 queue with 8 compute workers (on the same machine and
install). "Memory" is the wasm memory of the manager and of all compute workers at the end of
the queue:

| shots | shots in the queue | wall time | ms a pair | memory | one compute worker |
| --- | --- | --- | --- | --- | --- |
| 40 | 200 640 | 41 s | 255 ms | 467 MB | 29 to 36 MB |
| 160 | 802 560 | 83 s | 523 ms | 463 MB | 29 to 34 MB |
| 768 | 3 852 288 | 279 s | 1764 ms | 490 MB | 33 to 38 MB |

The time does not grow as fast as the lines, as each pair also reads and parses the collision
resource and builds the mesh, the BVH and the grids. The manager takes 211 MB at each step.

The manager prepares a target in 11 ms with 4 compute workers and in 25 ms with 16, as the
threads are busier: it loads the unit, finds the collision resource and encodes the packet.
At 16 workers, each pair takes 1.7 times as long as at 8. The machine has 8 cores, so two
workers share each core, and 16 workers give only 1.2 times the speed of 8.

Before, the manager also read and parsed each collision resource and put the nodes in the
packet. It then took 28 ms for a target with 4 workers and 45 ms with 16. With
the BT-5 and 16 workers it was busy 99 % of the time, the compute workers waited 18 % of the
time, and the queue took 57 s.

## Memory

Wasm memory never shrinks, so the peak sets the size of each worker. On the 2.61.0.42 install,
from open to the end of the M4A2 queue (headless Chromium):

| compute workers | one wasm per worker | manager and compute workers |
| --- | --- | --- |
| 4 | 801 MB | 323 MB |
| 8 | 1604 MB | 443 MB |
| 16 | 3205 MB | 670 MB |

With one wasm per worker, each worker took about 200 MB. Now the manager takes about 200 MB
and each compute worker 28 to 32 MB. The peak of the manager comes from `open`: the
containers unpack one at a time, and the parse of `char.vromfs.bin/config/wpcost.blk` makes a tree of about 90 MB for a
short time. After `open` the manager keeps about 25 MB of game data: the name maps, the `lang`
names and the files of `aces.vromfs.bin` that the simulation reads. A compute worker keeps the
tables, the shooter, and the packet, the collision resource, the mesh and the BVH of the
target that it fires at.
`compute.wasm` is 6.0 MB, `manager.wasm` 6.8 MB.

## Checks

    go vet ./... && GOOS=js GOARCH=wasm go vet ./...
    go test ./...

## License

Unlicense (public domain). See `LICENSE`.
