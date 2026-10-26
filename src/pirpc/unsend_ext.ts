// pitago-unsend: takes back a prompt the user cancelled before the model
// answered it (Claude Code parity: the text returns to the editor and the
// model never sees it).
//
// pi's RPC mode has no navigate_tree command, but an extension command can
// call ctx.navigateTree. Navigating to a user message moves the session leaf
// to that message's parent in the same session file, which is exactly what
// pi's own /tree does for "edit this message". The cancelled prompt stays in
// the tree as an abandoned branch and leaves the model's context.
//
// pitago loads this file with `pi -e <path>` and runs it as
// "/pitago-unsend <unix ms>". The argument is the moment pitago sent the
// prompt. Only user messages stamped at or after it are taken back, and
// nothing happens once the model has produced any output for them.
//
// No imports on purpose: the file is written to pitago's cache dir, where
// no package can be resolved.

function hasOutput(message: any): boolean {
  const content = message?.content;
  if (typeof content === "string") return content.trim() !== "";
  if (!Array.isArray(content)) return false;
  return content.some((block: any) => {
    if (block?.type === "text") return String(block.text ?? "").trim() !== "";
    if (block?.type === "thinking") return String(block.thinking ?? "").trim() !== "";
    return block?.type === "toolCall";
  });
}

// unsendTarget returns the id of the oldest user message on the current
// branch stamped at or after `since`, or undefined when there is none or
// when the model already answered after it.
function unsendTarget(branch: any[], since: number): string | undefined {
  let target: string | undefined;
  for (let i = branch.length - 1; i >= 0; i--) {
    const entry = branch[i];
    if (entry?.type === "compaction" || entry?.type === "branch_summary") break;
    if (entry?.type !== "message") continue; // labels, model changes, custom messages
    const message = entry.message;
    if (message?.role === "user") {
      if (Number(message.timestamp ?? 0) < since) break;
      target = entry.id;
      continue;
    }
    // pi's record of the run's system prompt, written before the prompt.
    if (message?.role === "system") continue;
    // An aborted or failed reply with nothing in it is not an answer.
    if (message?.role === "assistant" && !hasOutput(message)) continue;
    // Older than the prompt: an earlier turn, so the search is over.
    if (target) break;
    return undefined; // newer than the prompt: the model answered or a tool ran
  }
  return target;
}

export default function (pi: any) {
  pi.registerCommand("pitago-unsend", {
    description: "Internal to pitago: take back a prompt cancelled before the model answered",
    handler: async (args: string, ctx: any) => {
      const since = Number(String(args ?? "").trim());
      if (!Number.isFinite(since) || since <= 0) return;
      await ctx.waitForIdle();
      const target = unsendTarget(ctx.sessionManager.getBranch(), since);
      if (target) await ctx.navigateTree(target);
    },
  });
}
