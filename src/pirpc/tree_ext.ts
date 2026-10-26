// pitago-tree: pi's /tree actions for pitago's Session Tree.
//
// pi's RPC mode has no navigate_tree or label command, but an extension
// command can reach both: ctx.navigateTree is the same session.navigateTree
// pi's own /tree calls when an entry is picked (showTreeSelector), and
// pi.setLabel is the label edit (appendLabelChange).
//
// pitago loads this file with `pi -e <path>` and runs:
//   /pitago-tree {"targetId", "summarize", "customInstructions", "out"}
//   /pitago-tree-label {"entryId", "label"}
//
// A command's reply over RPC only says "handled", so the navigate command
// writes its outcome as JSON to the file named by "out":
//   {"cancelled": bool} after navigateTree returned, {"error": "..."} if it threw.
//
// No static imports on purpose: the file is written to pitago's cache dir,
// where no package can be resolved. node:fs is a builtin and is loaded
// lazily, so a runtime without it only loses the outcome report.

function parse(args: string): any {
  try {
    return JSON.parse(String(args ?? ""));
  } catch {
    return undefined;
  }
}

async function report(out: unknown, result: unknown): Promise<void> {
  if (typeof out !== "string" || out === "") return;
  try {
    const fs: any = await import("node:fs");
    fs.writeFileSync(out, JSON.stringify(result));
  } catch {
    // pitago treats a missing report as "outcome unknown".
  }
}

export default function (pi: any) {
  pi.registerCommand("pitago-tree", {
    description: "Internal to pitago: navigate the session tree",
    handler: async (args: string, ctx: any) => {
      const req = parse(args);
      if (!req || typeof req.targetId !== "string") return;
      try {
        await ctx.waitForIdle();
        const res = await ctx.navigateTree(req.targetId, {
          summarize: req.summarize === true,
          customInstructions: typeof req.customInstructions === "string" ? req.customInstructions : undefined,
        });
        await report(req.out, { cancelled: res?.cancelled === true });
      } catch (err) {
        await report(req.out, { error: err instanceof Error ? err.message : String(err) });
      }
    },
  });

  pi.registerCommand("pitago-tree-label", {
    description: "Internal to pitago: set or clear a session tree label",
    handler: async (args: string) => {
      const req = parse(args);
      if (!req || typeof req.entryId !== "string") return;
      const label = typeof req.label === "string" && req.label.trim() !== "" ? req.label.trim() : undefined;
      pi.setLabel(req.entryId, label);
    },
  });
}
