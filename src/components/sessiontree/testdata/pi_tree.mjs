// Runs pi's own TreeList over the scenarios on stdin and prints the rendered
// rows (styles stripped) per scenario, for parity_test.go.
//
// usage: node pi_tree.mjs <pi-coding-agent dist dir> < scenarios.json
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const dist = process.argv[2];
const load = (p) => import(pathToFileURL(`${dist}/${p}`).href);
const { TreeSelectorComponent } = await load("modes/interactive/components/tree-selector.js");
const theme = await load("modes/interactive/theme/theme.js");
const core = await load("core/keybindings.js");
const tui = await import(pathToFileURL(`${dist}/../node_modules/@earendil-works/pi-tui/dist/index.js`).href).catch(
  () => import(pathToFileURL(`${dist}/../../pi-tui/dist/index.js`).href),
);
theme.initTheme?.("dark");
tui.setKeybindings(new core.KeybindingsManager());

// Raw key data for each action, as a terminal sends it.
const keys = {
  up: "\x1b[A", down: "\x1b[B", left: "\x1b[D", right: "\x1b[C",
  fold: "\x1b[1;5D", unfold: "\x1b[1;5C", pageUp: "\x1b[5~", pageDown: "\x1b[6~",
  esc: "\x1b", backspace: "\x7f",
  ctrlD: "\x04", ctrlT: "\x14", ctrlU: "\x15", ctrlL: "\x0c", ctrlA: "\x01", ctrlO: "\x0f",
  labelTime: "T",
};
const strip = (s) => s.replace(/\x1b\[[0-9;]*m/g, "").replace(/\x1b\]8;;[^\x07]*\x07/g, "");
const out = [];
for (const sc of JSON.parse(readFileSync(0, "utf8"))) {
  const sel = new TreeSelectorComponent(sc.tree, sc.leaf ?? null, sc.rows, () => {}, () => {}, () => {}, sc.select ?? undefined, sc.mode);
  const list = sel.getTreeList();
  for (const op of sc.ops ?? []) {
    if (op.startsWith("type:")) list.handleInput(op.slice(5));
    else list.handleInput(keys[op]);
  }
  out.push(list.render(sc.width).map(strip));
}
process.stdout.write(JSON.stringify(out));
