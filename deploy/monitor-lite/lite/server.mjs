import { createHash } from "node:crypto";
import { snapshotAuthorized } from "./authorization.mjs";
import { createSnapshotReader } from "./snapshot-cache.mjs";
import { once } from "node:events";
import { readFile } from "node:fs/promises";
import http from "node:http";
import https from "node:https";
import { dirname, join } from "node:path";
import readline from "node:readline";
import tls from "node:tls";
import { fileURLToPath } from "node:url";

const HOST = process.env.HOST || "0.0.0.0";
const PORT = Number(process.env.PORT || "3100");
const REQUEST_TIMEOUT_MS = 8_000;
const SHANGHAI_OFFSET_MS = 8 * 60 * 60 * 1_000;
const ALERT_HOUR = 10;
const ALERT_MINUTE_LIMIT = 15;
const BALANCE_THRESHOLD = Number(process.env.ALERT_UPSTREAM_BALANCE_THRESHOLD || "10");

const UPSTREAMS = [
  { id: "gapi", name: "GAPI", baseUrl: "https://api.gapi.cc", adapter: "newapi", tokenEnv: "UPSTREAM_GAPI_ACCESS_TOKEN", userIdEnv: "UPSTREAM_GAPI_USER_ID" },
  { id: "u-gateway", name: "Hajimi", baseUrl: "https://hajimi.chat", adapter: "wallet", tokenEnv: "UPSTREAM_U_GATEWAY_API_KEY" },
  { id: "inroi", name: "Inroi", baseUrl: "https://www.inroi.shop", adapter: "wallet", tokenEnv: "UPSTREAM_INROI_API_KEY" },
  { id: "doro", name: "Doro", baseUrl: "https://doro.lol", adapter: "newapi", tokenEnv: "UPSTREAM_DORO_ACCESS_TOKEN", userIdEnv: "UPSTREAM_DORO_USER_ID" },
  { id: "token-api", name: "Token API", baseUrl: "https://token.xn--cjz.space", adapter: "wallet", tokenEnv: "UPSTREAM_TOKEN_API_KEY" },
  { id: "otok-api", name: "OtokAPI", baseUrl: "https://otokapi.com", adapter: "wallet", tokenEnv: "UPSTREAM_OTOK_API_KEY" },
  { id: "max-api", name: "max_api", baseUrl: "https://ai.max66.xyz", adapter: "wallet", tokenEnv: "UPSTREAM_MAX_API_KEY" },
  { id: "viapi", name: "Viapi", baseUrl: "https://api.viapi.cc", adapter: "wallet", tokenEnv: "UPSTREAM_VIAPI_API_KEY" },
  { id: "opr-tokents", name: "PaoluAPI", baseUrl: "https://opr.tokents.top", adapter: "wallet", tokenEnv: "UPSTREAM_OPR_TOKENTS_API_KEY" },
  { id: "hc-atom", name: "HC-ATOM 视频", baseUrl: "https://api-aigc.fzyinghe.com", adapter: "signed-business", tokenEnv: "UPSTREAM_HC_ATOM_BUSINESS_API_KEY", userIdEnv: "UPSTREAM_HC_ATOM_USER_ID", currency: "¥" },
].filter((definition) => (
  definition.id !== "hc-atom"
  || Boolean(
    String(process.env.UPSTREAM_HC_ATOM_BUSINESS_API_KEY || "").trim()
    && String(process.env.UPSTREAM_HC_ATOM_USER_ID || "").trim()
  )
));

const publicRoot = join(dirname(fileURLToPath(import.meta.url)), "public");
const staticFiles = new Map(await Promise.all([
  ["/", "index.html", "text/html; charset=utf-8"],
  ["/styles.css", "styles.css", "text/css; charset=utf-8"],
  ["/app.js", "app.js", "text/javascript; charset=utf-8"],
].map(async ([route, filename, contentType]) => [
  route,
  { body: await readFile(join(publicRoot, filename)), contentType },
])));

let lastAlertDay = "";
// Small private JSON cache; all HTTP callers pass authorization first.
const getSnapshot = createSnapshotReader(buildSnapshot, { ttlMs: 15_000 });

function asRecord(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}

function finiteValue(...values) {
  for (const value of values) {
    if (value === null || value === undefined || value === "") continue;
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return null;
}

function normalizedCurrency(value) {
  const currency = String(value || "$").trim();
  return currency.toUpperCase() === "USD" ? "$" : currency;
}

async function readJson(baseUrl, path, headers = {}, options = {}) {
  return new Promise((resolve, reject) => {
    let settled = false;
    let hardTimeout;
    const resolveOnce = (value) => {
      if (settled) return;
      settled = true;
      clearTimeout(hardTimeout);
      resolve(value);
    };
    const rejectOnce = (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(hardTimeout);
      reject(error);
    };
    const requestBody = options.body === undefined
      ? null
      : Buffer.from(JSON.stringify(options.body));
    const request = https.request(new URL(path, baseUrl), {
      method: options.method || "GET",
      agent: false,
      headers: {
        Accept: "application/json",
        Connection: "close",
        ...(requestBody ? {
          "Content-Type": "application/json; charset=utf-8",
          "Content-Length": requestBody.length,
        } : {}),
        ...headers,
      },
    }, (response) => {
      const chunks = [];
      let size = 0;
      response.on("data", (chunk) => {
        size += chunk.length;
        if (size > 1024 * 1024) {
          request.destroy(new Error("上游响应过大"));
          return;
        }
        chunks.push(chunk);
      });
      response.on("end", () => {
        try {
          const raw = Buffer.concat(chunks).toString("utf8");
          let payload;
          try {
            payload = asRecord(JSON.parse(raw || "{}"));
          } catch {
            if (!response.statusCode || response.statusCode < 200 || response.statusCode >= 300) {
              throw new Error(`HTTP ${response.statusCode || 500}`);
            }
            throw new Error("上游返回了非 JSON 数据");
          }
          if (!response.statusCode || response.statusCode < 200 || response.statusCode >= 300) {
            throw new Error(String(payload.message || asRecord(payload.error).message || `HTTP ${response.statusCode || 500}`));
          }
          resolveOnce(payload);
        } catch (error) {
          rejectOnce(error);
        }
      });
      response.on("aborted", () => rejectOnce(new Error("上游响应中断")));
      response.on("error", rejectOnce);
    });
    request.setTimeout(REQUEST_TIMEOUT_MS, () => request.destroy(new Error("上游请求超时")));
    request.on("error", rejectOnce);
    hardTimeout = setTimeout(
      () => request.destroy(new Error("上游请求超过绝对时限")),
      REQUEST_TIMEOUT_MS,
    );
    request.end(requestBody || undefined);
  });
}

function quotaData(payload) {
  const root = asRecord(payload);
  if (root.success === false) throw new Error(String(root.message || "查询失败"));
  const data = asRecord(root.data);
  return Object.keys(data).length ? data : root;
}

async function readNewApiBalance(definition, token, userId) {
  const headers = { Authorization: `Bearer ${token}` };
  if (userId) headers["New-Api-User"] = userId;
  const [statusPayload, selfPayload] = await Promise.all([
    readJson(definition.baseUrl, "/api/status"),
    readJson(definition.baseUrl, "/api/user/self", headers),
  ]);
  const status = quotaData(statusPayload);
  const account = quotaData(selfPayload);
  const quota = finiteValue(account.quota, account.remaining, account.balance);
  const quotaPerUnit = Math.max(1, finiteValue(status.quota_per_unit, 500_000));
  if (quota === null) throw new Error("余额接口未返回额度");
  return {
    balance: quota / quotaPerUnit,
    currency: normalizedCurrency(status.custom_currency_symbol || account.currency || "$"),
    planName: String(account.group || account.planName || "账户余额"),
  };
}

async function readWalletBalance(definition, token) {
  let primaryError;
  const headers = {
    Authorization: `Bearer ${token}`,
    "User-Agent": "cc-switch/1.0",
  };
  try {
    const payload = await readJson(
      definition.baseUrl,
      "/v1/usage?days=7&timezone=Asia%2FShanghai",
      headers,
    );
    const data = quotaData(payload);
    const wallet = asRecord(data.wallet);
    const quota = asRecord(data.quota);
    const balance = finiteValue(data.balance, data.remaining, wallet.balance, quota.remaining);
    if (balance !== null) {
      return {
        balance,
        currency: normalizedCurrency(data.unit || data.currency || "$"),
        planName: String(data.planName || data.plan_name || "钱包余额"),
      };
    }
  } catch (error) {
    primaryError = error;
    // Some compatible providers expose only the account self endpoint.
  }

  try {
    const payload = await readJson(
      definition.baseUrl,
      "/api/user/self",
      headers,
    );
    const data = quotaData(payload);
    const quota = finiteValue(data.quota, data.remaining, data.balance);
    if (quota === null) throw new Error("余额接口未返回额度");
    return {
      balance: quota / 500_000,
      currency: normalizedCurrency(data.unit || data.currency || "$"),
      planName: String(data.group || data.planName || "账户余额"),
    };
  } catch (fallbackError) {
    throw primaryError || fallbackError;
  }
}

async function readSignedBusinessBalance(definition, token, userId) {
  const parsedUserId = Number(userId);
  const requestUserId = Number.isSafeInteger(parsedUserId) ? parsedUserId : userId;
  const timestamp = Math.floor(Date.now() / 1_000);
  const signatureFields = { userId: requestUserId, timestamp };
  const stringA = Object.entries(signatureFields)
    .filter(([, value]) => value !== null && value !== undefined && value !== "")
    .sort(([left], [right]) => left < right ? -1 : left > right ? 1 : 0)
    .map(([key, value]) => `${key}=${value}`)
    .join("&");
  const sign = createHash("md5")
    .update(`${stringA}&key=${token}`)
    .digest("hex")
    .toUpperCase();
  const payload = await readJson(
    definition.baseUrl,
    "/business/reconcile/balance",
    {},
    { method: "POST", body: { ...signatureFields, sign } },
  );
  if (payload.code !== undefined && Number(payload.code) !== 200) {
    throw new Error(String(payload.msg || payload.message || `业务接口错误 ${payload.code}`));
  }
  const data = asRecord(payload.data);
  const balance = finiteValue(data.balance, payload.balance);
  if (balance === null) throw new Error("视频余额接口未返回余额");
  return {
    balance,
    currency: "¥",
    planName: "视频账户余额",
  };
}

async function readUpstream(definition) {
  const token = String(process.env[definition.tokenEnv] || "").trim();
  const userId = definition.userIdEnv
    ? String(process.env[definition.userIdEnv] || "").trim()
    : "";
  const configured = Boolean(token && (!definition.userIdEnv || userId));
  const common = {
    id: definition.id,
    name: definition.name,
    baseUrl: definition.baseUrl,
    threshold: BALANCE_THRESHOLD,
  };

  if (!configured) {
    return { ...common, status: "waiting", balance: null, currency: definition.currency || "$", planName: "等待凭据" };
  }

  try {
    let balance;
    if (definition.adapter === "newapi") {
      balance = await readNewApiBalance(definition, token, userId);
    } else if (definition.adapter === "signed-business") {
      balance = await readSignedBusinessBalance(definition, token, userId);
    } else {
      balance = await readWalletBalance(definition, token);
    }
    return {
      ...common,
      ...balance,
      status: balance.balance < BALANCE_THRESHOLD ? "low" : "ready",
    };
  } catch (error) {
    return {
      ...common,
      status: "error",
      balance: null,
      currency: definition.currency || "$",
      planName: "查询失败",
      error: error instanceof Error ? error.message : "未知错误",
    };
  }
}

async function buildSnapshot() {
  const accounts = await Promise.all(UPSTREAMS.map(readUpstream));
  const readyAccounts = accounts.filter((account) => account.balance !== null);
  const totalsByCurrency = readyAccounts.reduce((totals, account) => {
    totals[account.currency] = (totals[account.currency] || 0) + account.balance;
    return totals;
  }, {});
  return {
    generatedAt: new Date().toISOString(),
    refreshSeconds: 300,
    threshold: BALANCE_THRESHOLD,
    accounts,
    summary: {
      totalBalance: totalsByCurrency.$ || 0,
      totalsByCurrency,
      readyCount: accounts.filter((account) => account.status === "ready").length,
      lowCount: accounts.filter((account) => account.status === "low").length,
      errorCount: accounts.filter((account) => account.status === "error").length,
      configuredCount: accounts.filter((account) => account.status !== "waiting").length,
      accountCount: accounts.length,
    },
  };
}

function shanghaiClock(now = Date.now()) {
  const value = new Date(now + SHANGHAI_OFFSET_MS);
  return {
    year: value.getUTCFullYear(),
    month: value.getUTCMonth() + 1,
    day: value.getUTCDate(),
    hour: value.getUTCHours(),
    minute: value.getUTCMinutes(),
  };
}

function clockDay(clock) {
  return `${clock.year}-${String(clock.month).padStart(2, "0")}-${String(clock.day).padStart(2, "0")}`;
}

function configured(name) {
  const value = String(process.env[name] || "").trim();
  if (!value) throw new Error(`${name} 未配置`);
  return value;
}

function encodeHeader(value) {
  return `=?UTF-8?B?${Buffer.from(value).toString("base64")}?=`;
}

async function sendSmtp(subject, body) {
  const host = configured("SMTP_HOST");
  const port = Number(process.env.SMTP_PORT || "465");
  const user = configured("SMTP_USER");
  const pass = configured("SMTP_PASS");
  const recipient = configured("ALERT_EMAIL_TO");
  const socket = tls.connect({ host, port, servername: host, rejectUnauthorized: true });

  await Promise.race([
    once(socket, "secureConnect"),
    once(socket, "error").then(([error]) => Promise.reject(error)),
    new Promise((_, reject) => setTimeout(() => reject(new Error("SMTP 连接超时")), 8_000)),
  ]);

  const lines = readline.createInterface({ input: socket, crlfDelay: Infinity });
  const iterator = lines[Symbol.asyncIterator]();

  async function expect(expected) {
    const replies = [];
    while (true) {
      const { value, done } = await iterator.next();
      if (done) throw new Error("SMTP 连接已关闭");
      const line = String(value);
      replies.push(line);
      const match = line.match(/^(\d{3})([ -])/);
      if (!match || match[2] === "-") continue;
      const code = Number(match[1]);
      if (code !== expected) throw new Error(`SMTP ${code}: ${replies.join(" | ")}`);
      return;
    }
  }

  async function command(value, expected) {
    socket.write(`${value}\r\n`);
    await expect(expected);
  }

  try {
    await expect(220);
    await command("EHLO meteor-balance", 250);
    await command("AUTH LOGIN", 334);
    await command(Buffer.from(user).toString("base64"), 334);
    await command(Buffer.from(pass).toString("base64"), 235);
    await command(`MAIL FROM:<${user}>`, 250);
    await command(`RCPT TO:<${recipient}>`, 250);
    await command("DATA", 354);
    const message = [
      `From: Meteor Balance <${user}>`,
      `To: <${recipient}>`,
      `Subject: ${encodeHeader(subject)}`,
      `Date: ${new Date().toUTCString()}`,
      "MIME-Version: 1.0",
      "Content-Type: text/plain; charset=UTF-8",
      "Content-Transfer-Encoding: 8bit",
      "",
      body.replace(/\r?\n/g, "\r\n").replace(/^\./gm, ".."),
      ".",
    ].join("\r\n");
    socket.write(`${message}\r\n`);
    await expect(250);
    await command("QUIT", 221);
  } finally {
    lines.close();
    socket.destroy();
  }
}

async function checkAlerts(request) {
  const alertToken = configured("ALERT_CRON_TOKEN");
  if (request.headers.authorization !== `Bearer ${alertToken}`) {
    return { status: 401, payload: { success: false, message: "unauthorized" } };
  }

  const clock = shanghaiClock();
  if (clock.hour !== ALERT_HOUR || clock.minute >= ALERT_MINUTE_LIMIT) {
    return {
      status: 200,
      payload: { success: true, sent: false, skipped: "outside_alert_window", nextRun: "daily 10:00 Asia/Shanghai" },
    };
  }

  const today = clockDay(clock);
  if (lastAlertDay === today) {
    return { status: 200, payload: { success: true, sent: false, skipped: "already_processed_today" } };
  }

  const snapshot = await getSnapshot();
  const alerts = snapshot.accounts.flatMap((account) => {
    if (account.status === "error") {
      return [`${account.name} 余额查询失败：${account.error || "未知错误"}`];
    }
    if (account.status === "low") {
      return [`${account.name} 余额不足：${account.currency}${account.balance.toFixed(2)}，预警线 ${account.currency}${account.threshold.toFixed(2)}`];
    }
    return [];
  });
  if (!alerts.length) {
    lastAlertDay = today;
    return { status: 200, payload: { success: true, sent: false, alertCount: 0 } };
  }

  const text = [
    "Meteor Balance 检测到以下问题：",
    "",
    ...alerts.map((alert, index) => `${index + 1}. ${alert}`),
    "",
    `检测时间：${today} ${String(clock.hour).padStart(2, "0")}:${String(clock.minute).padStart(2, "0")}（北京时间）`,
    "此邮件由只读余额监控服务自动发送，未调用模型。",
  ].join("\n");
  await sendSmtp(`Meteor Balance 告警：${alerts.length} 项需要关注`, text);
  lastAlertDay = today;
  return { status: 200, payload: { success: true, sent: true, alertCount: alerts.length } };
}

function sendJson(response, status, payload) {
  const body = Buffer.from(JSON.stringify(payload));
  response.writeHead(status, {
    "Content-Type": "application/json; charset=utf-8",
    "Content-Length": body.length,
    "Cache-Control": "no-store",
    "X-Content-Type-Options": "nosniff",
  });
  response.end(body);
}

const server = http.createServer(async (request, response) => {
  try {
    const url = new URL(request.url || "/", "http://localhost");
    if (request.method === "GET" && (url.pathname === "/api/snapshot" || url.pathname === "/api/newapi/snapshot")) {
      const token = String(process.env.MONITOR_SNAPSHOT_TOKEN || "").trim();
      if (!token) {
        sendJson(response, 503, { success: false, message: "monitor authentication is not configured" });
        return;
      }
      if (!snapshotAuthorized(request.headers.authorization, token)) {
        response.setHeader("WWW-Authenticate", 'Bearer realm="meteor-monitor"');
        sendJson(response, 401, { success: false, message: "unauthorized" });
        return;
      }
      response.setHeader("Vary", "Authorization");
      sendJson(response, 200, await getSnapshot());
      return;
    }
    if (request.method === "POST" && (url.pathname === "/api/alerts/check" || url.pathname === "/api/newapi/alerts/check")) {
      const result = await checkAlerts(request);
      sendJson(response, result.status, result.payload);
      return;
    }
    if (request.method === "GET" && url.pathname === "/healthz") {
      sendJson(response, 200, { ok: true });
      return;
    }
    if ((request.method === "GET" || request.method === "HEAD") && url.pathname === "/") {
      // Browser access uses the main site's existing owner session. A service
      // token is never sent to a browser or embedded in a static asset.
      response.writeHead(303, {
        Location: "https://api.meteor21c.fun/admin/business-monitor",
        "Cache-Control": "no-store",
        "Referrer-Policy": "no-referrer",
      });
      response.end();
      return;
    }
    const asset = request.method === "GET" ? staticFiles.get(url.pathname) : null;
    if (asset) {
      response.writeHead(200, {
        "Content-Type": asset.contentType,
        "Content-Length": asset.body.length,
        "Cache-Control": url.pathname === "/" ? "no-cache" : "public, max-age=3600",
        "X-Content-Type-Options": "nosniff",
        "Referrer-Policy": "no-referrer",
      });
      response.end(asset.body);
      return;
    }
    sendJson(response, 404, { success: false, message: "not found" });
  } catch (error) {
    sendJson(response, 500, {
      success: false,
      message: error instanceof Error ? error.message : "internal error",
    });
  }
});

server.keepAliveTimeout = 5_000;
server.headersTimeout = 10_000;
server.requestTimeout = 15_000;
server.listen(PORT, HOST, () => {
  const address = server.address();
  const port = typeof address === "object" && address ? address.port : PORT;
  console.log(`METEOR_LITE_READY port=${port}`);
});

function shutdown() {
  server.close(() => process.exit(0));
  setTimeout(() => process.exit(1), 8_000).unref();
}

process.on("SIGTERM", shutdown);
process.on("SIGINT", shutdown);
