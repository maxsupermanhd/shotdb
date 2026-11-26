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

Open the page, press **Open game folder** and pick the **War Thunder install folder** (the
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
Cancel in either dialog does nothing. From the files of the folder, the page keeps only the files above. The
browser cannot remember the folder, so open or drop it on each visit. A datamine tree does
not work this way.

In these browsers, a folder without `aces.vromfs.bin` at its top gives an error, and the data
on the page stays. When you pick or drop a folder while the page still reads an older one,
the page opens only the newest folder.

Then pick the shooter in the box at the top, or click a card. The page does not pick a shooter
by itself, so no shots run until you pick one. Shots run from the shooter's RB battle rating
outwards, up and down. Hover a card for the details.

**Pause** next to the progress bar stops the queue: the compute workers finish the targets that they hold
and take no new one. **Resume** continues from the same place. A new shooter or round starts
its queue at once, also when the queue is paused.

The panel at the top right shows the shooter's gun and each round that the simulation fires:
type, calibre, mass, muzzle speed, flat penetration at 500 m and filler. The **best** column
counts the targets on which each round gave the best score. When you hover a card, the panel
marks the round that the card shows. Click a round to fire only that round. Click it again to
fire the best of all rounds.

**Workers** in the bar under the header sets the number of compute workers (Web Workers) that
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

**Compact** in the bar under the header shows each vehicle as a small square tile: the centre
of its picture in the result colour, and the front, side and rear bars in the bottom 4 px. The
page sets the tile size so that all shown nations fit the window width, at most 70 px (the
picture height and the bars). The trees scroll down. The picture keeps its natural size up to
1.25 times the tile width. For a smaller tile the page scales the picture down to 1.25 times
the tile width, so a tile always shows at least 80 % of the picture width. Below 8 px the page
keeps 8 px tiles and scrolls sideways. The page keeps the choice in `localStorage`.

URL parameters: `nations` (comma list), `speed` (targets that a compute worker holds at once:
one runs and the others wait; 1 to 8, default 2; the manager uses 2 for other values) and
`hover` (unit id to pin a tooltip once the queue is done).

### Command line

    go run ./cmd/shotsim -data <install or datamine> us_m4a2_sherman germ_pzkpfw_vi_ausf_h1_tiger

prints the result JSON that the page shows.

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
  and passes the user's actions to the manager. `src/types.ts` has the shapes of the JSON and
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
at several bearings from the target's nose (front -20, 0, 20; side -100, -80, 80, 100; rear
160, 180, 200 degrees), at 0 and 6 degrees down. Each direction lays a 28-cell grid over the
silhouette and keeps 8 of the cells that hit, spread evenly: 48 shot lines for the front,
64 for the side, 48 for the rear, per round. Every round of the shooter is fired along every
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
  `(kills + 0.4 x (pens + shatters)) / lines`; card score: front 0.5, side 0.3, rear 0.2.

## Workers

The page runs two wasm binaries, each in its own kind of Web Worker.

- **The manager** (`manager.wasm`, one worker) reads all game files: the containers, the BLK
  files, the language files, the trees, the tank packs, and it loads the vehicles. It also
  runs the queue: the order outwards from the shooter's BR, the forced round, the pause, the
  changes of shooter and round, and a queue number that drops the answers of an old queue. It
  gives the targets to the compute workers and collects the results.
- **The compute workers** (`compute.wasm`) read one part of one file per target, and no other
  file. Each one gets the global tables once per open (28 KB), the shooter once per queue, and
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

The cost is mostly the shot lines (48 to 64 per view and round) and the fragment rays behind
each perforation. A compute worker builds the mesh, the BVH and the lines of each target, and
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
