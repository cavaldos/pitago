// Pitago live session bridge: loopback-only, read-only SSE export for Pi.
// Load explicitly with: pi --extension /path/to/pitago/src/live/pitago-live-bridge.ts
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { mkdirSync, writeFileSync, unlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomBytes } from "node:crypto";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

type Client = ServerResponse<IncomingMessage>;
type Bridge = {
  server: Server;
  descriptorPath: string;
  clients: Set<Client>;
  revision: number;
  token: string;
  ctx: ExtensionContext;
};

let bridge: Bridge | undefined;
let starting: Promise<void> | undefined;

function runtimeDir(): string {
  if (process.env.PITAGO_LIVE_DESCRIPTORS) return process.env.PITAGO_LIVE_DESCRIPTORS;
  if (process.env.XDG_RUNTIME_DIR) return join(process.env.XDG_RUNTIME_DIR, "pitago-live");
  const uid = typeof process.getuid === "function" ? process.getuid() : 0;
  return join(tmpdir(), `pitago-live-${uid}`);
}

function sse(res: Client, event: string, id: number, data: unknown): void {
  res.write(`id: ${id}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
}

function snapshot(ctx: ExtensionContext, revision: number) {
  const sm = ctx.sessionManager;
  const header = sm.getHeader();
  const branch = sm.getBranch();
  const messages = branch.flatMap((entry) => {
    if (entry.type === "message") return [entry.message];
    if (entry.type === "custom_message") {
      return [{
        role: "custom" as const,
        timestamp: Date.parse(entry.timestamp),
        customType: entry.customType,
        content: entry.content,
        display: entry.display,
        details: entry.details,
      }];
    }
    return [];
  });
  return {
    revision,
    sessionId: header?.id ?? sm.getSessionId(),
    sessionName: sm.getSessionName() ?? "",
    leafId: sm.getLeafId() ?? "",
    cwd: ctx.cwd,
    model: ctx.model?.id ?? "",
    provider: ctx.model?.provider ?? "",
    thinkingLevel: ctx.thinkingLevel ?? "",
    isStreaming: !ctx.isIdle(),
    messages,
  };
}

async function stopBridge(): Promise<void> {
  const old = bridge;
  bridge = undefined;
  if (!old) return;
  for (const client of old.clients) client.end();
  old.clients.clear();
  try { unlinkSync(old.descriptorPath); } catch { /* already absent or best-effort cleanup */ }
  await new Promise<void>((resolve) => old.server.close(() => resolve()));
}

async function startBridge(ctx: ExtensionContext): Promise<void> {
  await stopBridge();
  const dir = runtimeDir();
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  const token = randomBytes(32).toString("hex");
  const state: Bridge = {
    // SAFETY: createServer below assigns the server before the bridge is exposed.
    server: undefined as unknown as Server,
    descriptorPath: join(dir, `session-${process.pid}-${Date.now()}.json`),
    clients: new Set(), revision: 0, token, ctx,
  };
  bridge = state;
  try {
  state.server = createServer((req, res) => {
    if (req.method !== "GET" || req.url?.split("?")[0] !== "/events" ||
        req.headers.authorization !== `Bearer ${token}`) {
      res.writeHead(401).end();
      return;
    }
    res.writeHead(200, {
      "Content-Type": "text/event-stream; charset=utf-8",
      "Cache-Control": "no-cache, no-transform",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    });
    res.write(": connected\n\n");
    state.clients.add(res);
    req.on("close", () => state.clients.delete(res));
    sse(res, "snapshot", state.revision, snapshot(state.ctx, state.revision));
  });
  state.server.on("error", (error) => {
    if (bridge === state) void ctx.ui.notify(`pitago live bridge: ${error.message}`, "error");
  });
  await new Promise<void>((resolve, reject) => {
    state.server.once("error", reject);
    state.server.listen(0, "127.0.0.1", () => {
      state.server.off("error", reject);
      resolve();
    });
  });
  const address = state.server.address();
  if (!address || typeof address === "string") {
    throw new Error("could not determine loopback bridge address");
  }
  const header = state.ctx.sessionManager.getHeader();
  // sessionName and model are additive picker hints. They are omitted when
  // unknown, so a descriptor from a build without them stays valid and the
  // Go side treats them as optional — the version is deliberately unchanged.
  const sessionName = state.ctx.sessionManager.getSessionName() ?? "";
  const model = state.ctx.model?.id ?? "";
  const descriptor: {
    version: number; endpoint: string; token: string; sessionId: string;
    cwd: string; pid: number; startedAt: number;
    sessionName?: string; model?: string;
  } = {
    version: 1,
    endpoint: `http://127.0.0.1:${address.port}/events`,
    token,
    sessionId: header?.id ?? state.ctx.sessionManager.getSessionId(),
    cwd: state.ctx.cwd,
    pid: process.pid,
    startedAt: Date.now(),
  };
  if (sessionName) descriptor.sessionName = sessionName;
  if (model) descriptor.model = model;
  writeFileSync(state.descriptorPath, JSON.stringify(descriptor), { mode: 0o600, flag: "wx" });
  const heartbeat = setInterval(() => {
    for (const client of state.clients) client.write(`: heartbeat ${Date.now()}\n\n`);
  }, 15_000);
  state.server.once("close", () => clearInterval(heartbeat));
  } catch (error) {
    await stopBridge();
    throw error;
  }
}

function forward(ctx: ExtensionContext, event: unknown): void {
  // pi rebuilds the shared UI object on every session rebind (setUIContext
  // wraps a fresh one), so the tap is re-checked on every event rather than
  // only at session_start: a rebind mid-turn must not silently drop the next
  // subagent notice. ctx.ui is a getter, so this always sees the current one.
  tapUI(ctx);
  const state = bridge;
  if (!state || state.ctx.sessionManager.getSessionId() !== ctx.sessionManager.getSessionId()) return;
  state.revision++;
  const data = event as Record<string, unknown>;
  for (const client of state.clients) sse(client, "pi", state.revision, data);
}

const UI_TAPPED = Symbol.for("pitago.live.uiTapped");

// `extension_ui_request` is an RPC-wire record only: pi writes it to stdout
// and never delivers it to pi.on handlers, so there is no event to forward.
// Every extension instead reaches the user through the shared ctx.ui object
// (runner.getUIContext() hands the SAME object to all of them), so tap those
// methods and re-emit the same record shape pitago already understands. This
// is the only channel for subagent/team traffic: pi has no subagent event.
// The tap is re-checked on every forwarded event (see forward) because pi
// rebuilds the UI context on every session rebind (new_session / fork /
// switch_session), and a stale tap would silently drop subagent notices.
function tapUI(ctx: ExtensionContext): void {
  // SAFETY: pi's ExtensionContext.ui is typed as the narrow UI surface
  // (notify/setStatus/setWidget/setTitle), which is exactly what this tap
  // rewrites. The assertion only widens the target so the five method keys
  // below can be looked up and replaced; the values are the same live
  // function properties pi itself installed on the shared object.
  const ui = ctx.ui as unknown as Record<string | symbol, unknown>;
  if (ui[UI_TAPPED]) return;
  const argsToRecord: Record<string, (args: unknown[]) => Record<string, unknown>> = {
    notify: (a) => ({ method: "notify", message: a[0], notifyType: a[1] }),
    setStatus: (a) => ({ method: "setStatus", statusKey: a[0], statusText: a[1] }),
    setWidget: (a) => ({
      method: "setWidget",
      widgetKey: a[0],
      // A widget's content is either a string array or a TUI component
      // factory. A factory cannot be serialized, so it is reported as
      // opaque rather than dropped: a consumer can then fall back to the
      // durable state it has (the pi-agent-team session records) instead of
      // concluding the session has no workers.
      widgetLines: Array.isArray(a[1]) ? a[1] : undefined,
      widgetOpaque: !Array.isArray(a[1]) && typeof a[1] === "function",
      widgetPlacement: (a[2] as { placement?: string } | undefined)?.placement,
    }),
    setTitle: (a) => ({ method: "setTitle", title: a[0] }),
    // pi's UI object exposes the camelCase setEditorText; only the WIRE method
    // is set_editor_text, which is the shape pitago's Go side parses. Tapping
    // the wire spelling here silently matched nothing and dropped every remote
    // editor prefill.
    setEditorText: (a) => ({ method: "set_editor_text", text: a[0] }),
  };
  for (const [method, toRecord] of Object.entries(argsToRecord)) {
    const original = ui[method];
    if (typeof original !== "function") continue;
    const originalFn = original as (...args: unknown[]) => unknown;
    ui[method] = (...args: unknown[]) => {
      try {
        forward(ctx, { type: "extension_ui_request", id: randomBytes(8).toString("hex"), ...toRecord(args) });
      } catch {
        // Observability must never break the calling extension.
      }
      return originalFn.apply(ui, args);
    };
  }
  ui[UI_TAPPED] = true;
}

export default function (pi: ExtensionAPI) {
  pi.on("session_start", async (_event, ctx) => {
    tapUI(ctx);
    starting = startBridge(ctx);
    try { await starting; } catch (error: any) {
      await ctx.ui.notify(`pitago live bridge failed: ${error?.message ?? error}`, "error");
    } finally { starting = undefined; }
  });
  pi.on("session_shutdown", async () => {
    if (starting) await starting.catch(() => {});
    await stopBridge();
  });

  pi.on("agent_start", (event, ctx) => { tapUI(ctx); forward(ctx, event); });
  pi.on("turn_start", (event, ctx) => forward(ctx, event));
  pi.on("message_start", (event, ctx) => forward(ctx, event));
  pi.on("message_update", (event, ctx) => forward(ctx, event));
  pi.on("message_end", (event, ctx) => { tapUI(ctx); forward(ctx, event); });
  pi.on("tool_execution_start", (event, ctx) => forward(ctx, event));
  pi.on("tool_execution_update", (event, ctx) => forward(ctx, event));
  pi.on("tool_execution_end", (event, ctx) => forward(ctx, event));
  pi.on("agent_settled", (event, ctx) => forward(ctx, event));
  pi.on("agent_end", (event, ctx) => forward(ctx, event));
  pi.on("turn_end", (event, ctx) => forward(ctx, event));
  pi.on("tool_call", (event, ctx) => forward(ctx, event));
  pi.on("tool_result", (event, ctx) => forward(ctx, event));
  pi.on("session_info_changed", (event, ctx) => forward(ctx, event));
}
