// Local Socket.IO server for the Go SDK's conformance tests (stream_test.go).
//
// It runs the same socket.io as the Owls Insight API (4.8.1, engine.io 6.6.4,
// ws 8.17.1) with the API's transport settings: websocket only, the same
// permessage-deflate options, and the same connect_error shapes. Only the ping
// timings are shortened (PING_INTERVAL_MS, PING_TIMEOUT_MS) so liveness can be
// tested in seconds. It listens on 127.0.0.1 only; no Owls Insight server is
// involved.
//
// socket.io is loaded from the first of: $OWLS_API_REPO/node_modules (a checkout
// of the API server), conformance/node_modules (pinned by package.json and
// package-lock.json; install it with `npm ci` in conformance/, or `make
// conformance`), then ../nba-odds-app/node_modules next to this repository.
//
// It prints {"port": N, ...} on stdout once listening, or {"skip": "why"} when
// socket.io cannot be found, is driven through POST /control with a JSON body
// {op, ...}, and exits when stdin closes.
//
// Never point a test at the production API: it blocks an IP address that makes
// too many WebSocket connection attempts, REST included.
import fs from "node:fs";
import { createServer } from "node:http";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const candidates = [process.env.OWLS_API_REPO, here, path.resolve(here, "..", "..", "nba-odds-app")].filter(Boolean);

let Server = null;
let found = null;
for (const dir of candidates) {
  const modules = path.resolve(dir, "node_modules");
  const pkg = path.join(modules, "socket.io", "package.json");
  if (!fs.existsSync(pkg)) continue;
  ({ Server } = createRequire(path.join(modules, "noop.js"))("socket.io"));
  // The engine.io that socket.io itself loads.
  const engineMain = createRequire(pkg).resolve("engine.io");
  found = {
    modules,
    socketIo: JSON.parse(fs.readFileSync(pkg, "utf8")).version,
    engineIo: JSON.parse(fs.readFileSync(path.join(path.dirname(engineMain), "..", "package.json"), "utf8")).version,
  };
  break;
}
if (!Server) {
  const where = candidates.map((c) => path.resolve(c, "node_modules")).join(", ");
  console.log(JSON.stringify({
    skip: `socket.io not found in ${where}; run \`npm ci\` in conformance/ (or \`make conformance\`), or set OWLS_API_REPO to a checkout of the API server with node_modules`,
  }));
  process.exit(0);
}

const pingInterval = Number(process.env.PING_INTERVAL_MS || 25000);
const pingTimeout = Number(process.env.PING_TIMEOUT_MS || 120000);

const state = {
  attempts: [], // every middleware call: {at, auth, ua, apiKey, deflateOffered, deflateNegotiated, refused}
  connections: [], // admitted sockets: {id, at, events: [{at, name, args}], connected, reason}
  blocked: [], // when each upgrade was refused by allowRequest
  rejected: [], // when each upgrade was answered with an HTTP error by rejectUpgrades
};
const sockets = new Map(); // id -> live socket
let refuse = null; // {count, message, data, sequence, n}; count < 0 means every attempt
let blockUpgrades = null; // {count, message}
let rejectUpgrades = null; // {count, status, retryAfter, body}
let noAck = { count: 0, event: null }; // drop the next `count` acks (of `event` only, when set)
let dropAfterMs = null;

function extensionsOf(socket) {
  const ws = socket.conn && socket.conn.transport && socket.conn.transport.socket;
  const ext = ws && ws._extensions;
  return !!(ext && ext["permessage-deflate"]);
}

const http = createServer((req, res) => {
  if (req.method !== "POST" || req.url !== "/control") {
    res.writeHead(404).end();
    return;
  }
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    let out;
    try {
      out = control(JSON.parse(body || "{}"));
    } catch (err) {
      res.writeHead(500, { "content-type": "application/json" }).end(JSON.stringify({ error: String(err) }));
      return;
    }
    res.writeHead(200, { "content-type": "application/json" }).end(JSON.stringify(out ?? { ok: true }));
  });
});

const io = new Server(http, {
  transports: ["websocket"],
  pingInterval,
  pingTimeout,
  perMessageDeflate: {
    threshold: 1024,
    zlibDeflateOptions: { level: 1 },
    serverNoContextTakeover: true,
    clientNoContextTakeover: true,
  },
  // The API's pre-handshake per-IP block.
  allowRequest: (_req, callback) => {
    if (blockUpgrades && blockUpgrades.count !== 0) {
      if (blockUpgrades.count > 0) blockUpgrades.count--;
      state.blocked.push(Date.now());
      callback(blockUpgrades.message, false);
      return;
    }
    callback(null, true);
  },
});

// Put rejectUpgrades in front of engine.io's own upgrade handler.
const upgradeListeners = http.listeners("upgrade");
http.removeAllListeners("upgrade");
http.on("upgrade", (req, socket, head) => {
  const r = rejectUpgrades;
  if (r && r.count !== 0) {
    if (r.count > 0) r.count--;
    state.rejected.push(Date.now());
    const retry = r.retryAfter != null ? `Retry-After: ${r.retryAfter}\r\n` : "";
    socket.end(
      `HTTP/1.1 ${r.status} Rejected\r\n${retry}Content-Type: text/plain\r\n` +
        `Content-Length: ${Buffer.byteLength(r.body)}\r\nConnection: close\r\n\r\n${r.body}`,
    );
    return;
  }
  for (const l of upgradeListeners) l.call(http, req, socket, head);
});

io.use((socket, next) => {
  const h = socket.handshake;
  const attempt = {
    at: Date.now(),
    auth: h.auth,
    ua: h.headers["user-agent"] || null,
    apiKey: h.query.apiKey || null,
    deflateOffered: /permessage-deflate/.test(h.headers["sec-websocket-extensions"] || ""),
    deflateNegotiated: extensionsOf(socket),
    refused: false,
  };
  state.attempts.push(attempt);
  if (refuse && refuse.count !== 0) {
    if (refuse.count > 0) refuse.count--;
    attempt.refused = true;
    // A sequence of {message, data} is used in turn, one per refused attempt.
    const r = refuse.sequence ? refuse.sequence[refuse.n++ % refuse.sequence.length] : refuse;
    next(Object.assign(new Error(r.message ?? "refused"), { data: r.data }));
    return;
  }
  next();
});

const PROPS_SUBSCRIBE = /^subscribe-(?:(.+)-)?props$/;

io.on("connection", (socket) => {
  const rec = { id: socket.id, at: Date.now(), events: [], connected: true, reason: null };
  state.connections.push(rec);
  sockets.set(socket.id, socket);
  let current = null;

  socket.onAny((name, ...args) => {
    rec.events.push({ at: Date.now(), name, args });
    const ack = (event, payload) => {
      if (noAck.count > 0 && (!noAck.event || noAck.event === event)) {
        noAck.count--;
        return;
      }
      socket.emit(event, payload);
    };
    if (name === "subscribe") {
      current = args[0] ?? {};
      ack("subscribed", current);
    } else if (name === "update-subscription") {
      current = { ...(current ?? {}), ...(args[0] ?? {}) };
      ack("subscribed", current);
    } else {
      const m = PROPS_SUBSCRIBE.exec(name);
      if (m) ack(m[1] ? `${m[1]}-props-subscribed` : "props-subscribed", args[0] ?? {});
    }
  });
  socket.on("disconnect", (reason) => {
    rec.connected = false;
    rec.reason = reason;
    sockets.delete(socket.id);
  });
  if (dropAfterMs != null) {
    const ms = dropAfterMs;
    setTimeout(() => {
      if (dropAfterMs != null && socket.connected) socket.conn.close();
    }, ms);
  }
});

function control(cmd) {
  switch (cmd.op) {
    case "state":
      return state;
    case "refuse":
      refuse = { count: cmd.count ?? -1, message: cmd.message ?? "refused", data: cmd.data, sequence: cmd.sequence, n: 0 };
      return;
    case "admit":
      refuse = null;
      return;
    case "blockUpgrades":
      blockUpgrades = { count: cmd.count ?? -1, message: cmd.message ?? "Too many connection attempts" };
      return;
    case "noAck":
      noAck = { count: cmd.count ?? 1, event: cmd.event ?? null };
      return;
    case "rejectUpgrades":
      // Answer the next `count` upgrades with an HTTP error before engine.io sees
      // them, as the API's edge rate limit does (429, optionally with Retry-After).
      rejectUpgrades = {
        count: cmd.count ?? -1,
        status: cmd.status ?? 429,
        retryAfter: cmd.retryAfter ?? null,
        body: cmd.body ?? "",
      };
      return;
    case "dropAfter":
      dropAfterMs = cmd.ms ?? null;
      return;
    case "drop":
      // Close the transport, as a network drop or a server restart does (no 41).
      for (const s of sockets.values()) {
        if (cmd.terminate) s.conn.transport.socket.terminate();
        else s.conn.close();
      }
      return;
    case "disconnect":
      // A server-initiated Socket.IO disconnect: 41, then the transport closes.
      for (const s of sockets.values()) s.disconnect(true);
      return;
    case "emit": {
      const payload = cmd.size != null ? { blob: "x".repeat(cmd.size) } : cmd.payload;
      const times = cmd.times ?? 1;
      for (const s of sockets.values()) for (let i = 0; i < times; i++) s.emit(cmd.event, payload);
      return;
    }
    case "stallPings":
      // Stop pinging AND stop waiting for pongs, so only the client can notice.
      for (const s of sockets.values()) {
        const c = s.conn;
        clearTimeout(c.pingIntervalTimer);
        clearTimeout(c.pingTimeoutTimer);
        c.schedulePing = () => {};
        c.resetPingTimeout = () => {};
        const send = c.sendPacket.bind(c);
        c.sendPacket = (type, ...rest) => (type === "ping" ? undefined : send(type, ...rest));
      }
      return;
    default:
      throw new Error(`unknown op ${cmd.op}`);
  }
}

http.listen(0, "127.0.0.1", () => {
  process.stdout.write(JSON.stringify({ port: http.address().port, ...found }) + "\n");
});
process.stdin.on("end", () => process.exit(0));
process.stdin.on("close", () => process.exit(0));
process.stdin.resume();
