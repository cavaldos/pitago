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
  const descriptor = {
    version: 1,
    endpoint: `http://127.0.0.1:${address.port}/events`,
    token,
    sessionId: header?.id ?? state.ctx.sessionManager.getSessionId(),
    cwd: state.ctx.cwd,
    pid: process.pid,
    startedAt: Date.now(),
  };
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
  const state = bridge;
  if (!state || state.ctx.sessionManager.getSessionId() !== ctx.sessionManager.getSessionId()) return;
  state.revision++;
  const data = event as Record<string, unknown>;
  for (const client of state.clients) sse(client, "pi", state.revision, data);
}

export default function (pi: ExtensionAPI) {
  pi.on("session_start", async (_event, ctx) => {
    starting = startBridge(ctx);
    try { await starting; } catch (error: any) {
      await ctx.ui.notify(`pitago live bridge failed: ${error?.message ?? error}`, "error");
    } finally { starting = undefined; }
  });
  pi.on("session_shutdown", async () => {
    if (starting) await starting.catch(() => {});
    await stopBridge();
  });

  pi.on("agent_start", (event, ctx) => forward(ctx, event));
  pi.on("turn_start", (event, ctx) => forward(ctx, event));
  pi.on("message_start", (event, ctx) => forward(ctx, event));
  pi.on("message_update", (event, ctx) => forward(ctx, event));
  pi.on("message_end", (event, ctx) => forward(ctx, event));
  pi.on("tool_execution_start", (event, ctx) => forward(ctx, event));
  pi.on("tool_execution_update", (event, ctx) => forward(ctx, event));
  pi.on("tool_execution_end", (event, ctx) => forward(ctx, event));
  pi.on("agent_settled", (event, ctx) => forward(ctx, event));
  pi.on("agent_end", (event, ctx) => forward(ctx, event));
  pi.on("session_info_changed", (event, ctx) => forward(ctx, event));
}
