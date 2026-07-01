#!/usr/bin/env node
/**
 * End-to-end smoke test: launch the built MCP server over stdio, run the JSON-RPC
 * handshake, then exercise tools/list + real calls against the live Evolution API.
 * Reads EVO_* from the environment.
 */
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const serverPath = join(root, "build", "index.js");

const child = spawn("node", [serverPath], {
  stdio: ["pipe", "pipe", "inherit"],
  env: process.env,
});

let buf = "";
const pending = new Map();
let nextId = 1;

child.stdout.on("data", (chunk) => {
  buf += chunk.toString();
  let nl;
  while ((nl = buf.indexOf("\n")) >= 0) {
    const line = buf.slice(0, nl).trim();
    buf = buf.slice(nl + 1);
    if (!line) continue;
    let msg;
    try { msg = JSON.parse(line); } catch { continue; }
    if (msg.id && pending.has(msg.id)) {
      pending.get(msg.id)(msg);
      pending.delete(msg.id);
    }
  }
});

function rpc(method, params) {
  const id = nextId++;
  return new Promise((resolve) => {
    pending.set(id, resolve);
    child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n");
  });
}

function notify(method, params) {
  child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method, params }) + "\n");
}

function toolText(res) {
  const c = res?.result?.content?.[0]?.text ?? JSON.stringify(res?.result ?? res?.error);
  return c.length > 600 ? c.slice(0, 600) + " …" : c;
}

const fail = (m) => { console.error("❌ " + m); child.kill(); process.exit(1); };

try {
  const init = await rpc("initialize", {
    protocolVersion: "2024-11-05",
    capabilities: {},
    clientInfo: { name: "smoke", version: "0.0.0" },
  });
  if (!init.result) fail("initialize failed: " + JSON.stringify(init));
  console.log("✓ initialize:", init.result.serverInfo?.name);
  notify("notifications/initialized", {});

  const list = await rpc("tools/list", {});
  const tools = list.result?.tools ?? [];
  console.log(`✓ tools/list: ${tools.length} tools (e.g. ${tools.slice(0, 5).map((t) => t.name).join(", ")} …)`);
  if (tools.length < 30) fail("expected ~50 tools, got " + tools.length);

  const instances = await rpc("tools/call", { name: "evo_instance_list", arguments: {} });
  console.log("✓ evo_instance_list →", toolText(instances), instances.result?.isError ? "(isError)" : "");

  const state = await rpc("tools/call", { name: "evo_instance_state", arguments: {} });
  console.log("✓ evo_instance_state →", toolText(state), state.result?.isError ? "(isError)" : "");

  const chats = await rpc("tools/call", { name: "evo_find_chats", arguments: {} });
  console.log("✓ evo_find_chats →", toolText(chats), chats.result?.isError ? "(isError)" : "");

  const contacts = await rpc("tools/call", { name: "evo_find_contacts", arguments: {} });
  console.log("✓ evo_find_contacts →", toolText(contacts), contacts.result?.isError ? "(isError)" : "");

  console.log("\n✅ smoke test completed");
  child.kill();
  process.exit(0);
} catch (e) {
  fail(String(e));
}
