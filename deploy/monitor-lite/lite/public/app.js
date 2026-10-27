const grid = document.querySelector("#accountGrid");
const refreshButton = document.querySelector("#refreshButton");
const totalBalance = document.querySelector("#totalBalance");
const readyCount = document.querySelector("#readyCount");
const readyDetail = document.querySelector("#readyDetail");
const attentionCount = document.querySelector("#attentionCount");
const countdown = document.querySelector("#countdown");
const updatedAt = document.querySelector("#updatedAt");
const threshold = document.querySelector("#threshold");
const errorMessage = document.querySelector("#errorMessage");

let refreshSeconds = 300;
let remainingSeconds = refreshSeconds;
let loading = false;

function money(currency, value) {
  if (value === null || value === undefined) return "—";
  const symbol = currency === "$" ? "$" : `${currency} `;
  return `${symbol}${Number(value).toLocaleString("zh-CN", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`;
}

function totalsText(totals) {
  const entries = Object.entries(totals || {}).sort(([left], [right]) => {
    if (left === "$") return -1;
    if (right === "$") return 1;
    return left.localeCompare(right, "zh-CN");
  });
  if (!entries.length) return "—";
  return entries.map(([currency, value]) => money(currency, value)).join(" + ");
}

function statusMeta(status) {
  return {
    ready: "正常",
    low: "余额偏低",
    error: "查询失败",
    waiting: "等待凭据",
  }[status] || "未知";
}

function accountCard(account) {
  const article = document.createElement("article");
  article.className = `account-card status-${account.status}`;

  const top = document.createElement("div");
  top.className = "account-top";
  const title = document.createElement("h3");
  title.className = "account-name";
  title.textContent = account.name;
  const badge = document.createElement("span");
  badge.className = "status";
  badge.textContent = statusMeta(account.status);
  top.append(title, badge);

  const label = document.createElement("p");
  label.className = "balance-label";
  label.textContent = "可用余额";
  const value = document.createElement("p");
  value.className = "balance-value";
  value.textContent = money(account.currency, account.balance);
  const plan = document.createElement("p");
  plan.className = "account-plan";
  plan.textContent = account.planName || "账户余额";
  const host = document.createElement("p");
  host.className = "account-host";
  host.textContent = new URL(account.baseUrl).hostname;

  article.append(top, label, value, plan, host);
  if (account.error) {
    const error = document.createElement("p");
    error.className = "account-error";
    error.textContent = account.error;
    article.append(error);
  }
  return article;
}

function render(snapshot) {
  const summary = snapshot.summary;
  totalBalance.textContent = totalsText(summary.totalsByCurrency || { $: summary.totalBalance });
  readyCount.textContent = `${summary.readyCount}/${summary.accountCount}`;
  readyDetail.textContent = `${summary.configuredCount} 个账户已配置`;
  attentionCount.textContent = String(summary.lowCount + summary.errorCount);
  threshold.textContent = Number(snapshot.threshold).toLocaleString("zh-CN", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
  updatedAt.textContent = `更新于 ${new Date(snapshot.generatedAt).toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  })}`;
  refreshSeconds = Number(snapshot.refreshSeconds) || 300;
  remainingSeconds = refreshSeconds;

  grid.replaceChildren(...snapshot.accounts.map(accountCard));
}

async function load() {
  if (loading) return;
  loading = true;
  refreshButton.disabled = true;
  refreshButton.classList.add("loading");
  errorMessage.hidden = true;

  try {
    const response = await fetch("/api/snapshot", { cache: "no-store" });
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.message || `HTTP ${response.status}`);
    render(payload);
  } catch (error) {
    errorMessage.textContent = `刷新失败：${error instanceof Error ? error.message : "未知错误"}`;
    errorMessage.hidden = false;
  } finally {
    loading = false;
    refreshButton.disabled = false;
    refreshButton.classList.remove("loading");
  }
}

function tick() {
  remainingSeconds = Math.max(0, remainingSeconds - 1);
  const minutes = String(Math.floor(remainingSeconds / 60)).padStart(2, "0");
  const seconds = String(remainingSeconds % 60).padStart(2, "0");
  countdown.textContent = `${minutes}:${seconds}`;
  if (remainingSeconds === 0) load();
}

refreshButton.addEventListener("click", load);
setInterval(tick, 1_000);
load();
