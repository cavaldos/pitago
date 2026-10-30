// pitago OAuth bridge: runs one pi subscription login (Claude Pro/Max,
// ChatGPT, Copilot, ...) with pi's OWN provider code, so pitago never
// re-implements any OAuth flow. JSONL protocol:
//   stdout  {type:"providers", list:[{id,name,label}]}        (op "list")
//           {type:"auth_url", url, instructions}
//           {type:"device_code", userCode, verificationUri}
//           {type:"info"|"progress", message}
//           {type:"prompt", id, kind, message, placeholder, options}
//           {type:"prompt_cancel", id}
//           {type:"done"} | {type:"error", message}
//   stdin   {type:"answer", id, value} | {type:"cancel"}
// argv: --pi-root <pi-coding-agent pkg dir> (list | login <provider>)
import { existsSync } from "node:fs";
import { join, dirname } from "node:path";
import { pathToFileURL } from "node:url";
import { randomUUID } from "node:crypto";
import { createInterface } from "node:readline";

const args = process.argv.slice(2);
const flag = (n) => {
  const i = args.indexOf(n);
  return i >= 0 ? args[i + 1] : undefined;
};
const root = flag("--pi-root");
const rest = args.filter((a, i) => a !== "--pi-root" && args[i - 1] !== "--pi-root");
const op = rest[0];
const providerId = rest[1];

const out = (o) => process.stdout.write(JSON.stringify(o) + "\n");
const fail = (e) => {
  out({ type: "error", message: e instanceof Error ? e.message : String(e) });
  process.exit(1);
};

const imp = (p) => import(pathToFileURL(p).href);

function piAiProviders() {
  const cands = [
    join(root, "node_modules", "@earendil-works", "pi-ai", "dist", "providers", "all.js"),
    join(dirname(root), "pi-ai", "dist", "providers", "all.js"),
  ];
  for (const c of cands) if (existsSync(c)) return c;
  throw new Error("pi-ai not found next to " + root);
}

async function oauthProviders() {
  const { builtinProviders } = await imp(piAiProviders());
  return builtinProviders().filter((p) => p.auth?.oauth?.login);
}

async function deviceId() {
  try {
    const { SettingsManager } = await imp(join(root, "dist", "core", "settings-manager.js"));
    return SettingsManager.create(process.cwd()).getOrCreateDeviceId();
  } catch {
    return randomUUID();
  }
}

async function list() {
  const ps = await oauthProviders();
  out({
    type: "providers",
    list: ps.map((p) => ({ id: p.id, name: p.auth.oauth.name || p.name, label: p.auth.oauth.loginLabel || "" })),
  });
}

// Set up HTTP exactly like pi does at startup. Importing pi's modules pulls
// in npm undici as the global dispatcher; without pi's configure step,
// Node 26's built-in fetch then returns gzip bodies undecoded and device-code
// logins (Copilot, xAI, Meta, Kimi, Radius) fail on JSON parsing. This also
// gives the login pi's HTTP(S)_PROXY support.
async function configureHttp() {
  const http = await imp(join(root, "dist", "core", "http-dispatcher.js"));
  try {
    const { SettingsManager } = await imp(join(root, "dist", "core", "settings-manager.js"));
    const sm = SettingsManager.create(process.cwd());
    // Same two calls as pi's main.js.
    http.applyHttpProxySettings(sm.getGlobalSettings().httpProxy);
    http.configureHttpDispatcher(sm.getHttpIdleTimeoutMs());
  } catch {
    http.configureHttpDispatcher();
  }
}

async function login() {
  await configureHttp();
  const ps = await oauthProviders();
  const provider = ps.find((p) => p.id === providerId);
  if (!provider) throw new Error("no OAuth login for provider: " + providerId);
  const { AuthStorage } = await imp(join(root, "dist", "core", "auth-storage.js"));
  const store = AuthStorage.create();

  const ctl = new AbortController();
  const pending = new Map(); // prompt id -> {resolve, reject}
  let seq = 0;
  const rl = createInterface({ input: process.stdin });
  rl.on("line", (line) => {
    let msg;
    try {
      msg = JSON.parse(line);
    } catch {
      return;
    }
    if (msg.type === "cancel") {
      ctl.abort(new Error("Login cancelled"));
      for (const p of pending.values()) p.reject(new Error("Login cancelled"));
      pending.clear();
    } else if (msg.type === "answer" && pending.has(msg.id)) {
      pending.get(msg.id).resolve(String(msg.value ?? ""));
      pending.delete(msg.id);
    }
  });
  // pitago closing stdin = the user left: abort the flow.
  rl.on("close", () => ctl.abort(new Error("Login cancelled")));

  const prompt = (p) =>
    new Promise((resolve, reject) => {
      const id = String(++seq);
      pending.set(id, { resolve, reject });
      out({
        type: "prompt",
        id,
        kind: p.type,
        message: p.message || "",
        placeholder: p.placeholder || "",
        options: p.type === "select" ? p.options.map((o) => ({ id: o.id, label: o.label })) : undefined,
      });
      p.signal?.addEventListener(
        "abort",
        () => {
          if (pending.delete(id)) {
            out({ type: "prompt_cancel", id });
            reject(new Error("Login cancelled"));
          }
        },
        { once: true },
      );
    });

  const notify = (e) => {
    if (e.type === "auth_url") out({ type: "auth_url", url: e.url, instructions: e.instructions || "" });
    else if (e.type === "device_code") out({ type: "device_code", userCode: e.userCode, verificationUri: e.verificationUri });
    else out({ type: e.type === "info" ? "info" : "progress", message: e.message || "" });
  };

  const id = await deviceId();
  const credential = await provider.auth.oauth.login({ signal: ctl.signal, prompt, notify }, { getDeviceId: () => id });
  await store.modify(provider.id, async () => credential);
  out({ type: "done" });
  rl.close();
  process.exit(0);
}

if (!root) fail("missing --pi-root");
(op === "list" ? list() : op === "login" ? login() : Promise.reject(new Error("usage: list | login <provider>"))).catch(fail);
