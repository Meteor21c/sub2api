(() => {
  "use strict";

  const API_BASE = window.location.origin.replace(/\/$/, "");
  const IMAGE_FALLBACK = ["gpt-image-2", "gpt-image-2-pro"];
  const VIDEO_FALLBACK = [
    "cheap-seedance-2.0", "cheap-seedance-2.0-fast", "cheap-seedance-2.0-mini",
    "doubao-seedance-2.0", "doubao-seedance-2.0-fast", "doubao-seedance-2.0-mini", "doubao-seedance-2.5",
    "kling-v3", "kling-v3-omni",
  ];
  const state = {
    image: { key: "", models: [], busy: false, history: [], objectUrls: [], scope: "", restoreEpoch: 0 },
    video: { key: "", models: [], busy: false, taskId: "", history: [], polling: new Set(), pollEpoch: 0, objectUrls: new Map(), scope: "" },
  };

  const $ = (id) => document.getElementById(id);
  const accountKeys = MeteorAccountKeys.createClient({
    readToken: () => localStorage.getItem("auth_token") || "",
    fetchJSON: async (path, token) => {
      const response = await fetch(path, { headers: { Authorization: `Bearer ${token}` }, cache: "no-store" });
      if (!response.ok) throw new Error(response.status === 401 ? "登录已过期，请重新登录。" : "读取密钥失败，请稍后重试。");
      const body = await response.json();
      if (body.code !== 0) throw new Error("读取密钥失败，请稍后重试。");
      return body.data;
    },
  });
  const mediaHistory = MeteorMediaHistory;
  const selectedKey = kind => accountKeys.get(kind, $(`${kind}-key`).value);
  const sleep = (ms) => new Promise((resolve) => window.setTimeout(resolve, ms));

  function updateImageOrientations() {
    const tier = $("image-tier").value;
    const select = $("image-orientation");
    const selected = select.value;
    select.replaceChildren();
    for (const orientation of MeteorImageOptions.orientationsForTier(tier)) {
      const option = document.createElement("option");
      option.value = orientation;
      option.textContent = MeteorImageOptions.ORIENTATION_LABELS[orientation] || orientation;
      select.append(option);
    }
    if ([...select.options].some(option => option.value === selected)) select.value = selected;
  }

  function updateImageOptions() {
    const select = $("image-tier");
    const selected = select.value;
    const group = accountKeys.getGroup("image", $("image-key").value);
    const tiers = MeteorImageOptions.tiersForGroup(group);
    select.replaceChildren();
    for (const tier of tiers) {
      const price = MeteorImageOptions.formatPrice(group?.imagePrices?.[tier]);
      const option = document.createElement("option");
      option.value = tier;
      option.textContent = price ? `${tier} · ${price}/张` : tier;
      select.append(option);
    }
    if (tiers.includes(selected)) select.value = selected;
    updateImageOrientations();
  }

  class ApiError extends Error {
    constructor(message, status = 0, data = null) {
      super(message);
      this.name = "ApiError";
      this.status = status;
      this.data = data;
    }
  }

  function errorMessage(data, fallback = "请求失败") {
    if (typeof data === "string" && data.trim()) return data.trim().slice(0, 600);
    const error = data?.error;
    return String(error?.message || data?.message || error?.code || fallback).slice(0, 600);
  }

  async function request(path, options = {}, key = "") {
    if (key && !["image", "video"].some(kind => accountKeys.owns(kind, key))) throw new ApiError("登录状态或密钥已变化，请重新读取。");
    const headers = new Headers(options.headers || {});
    if (key) headers.set("Authorization", `Bearer ${key}`);
    if (options.body && !(options.body instanceof FormData) && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    const controller = new AbortController();
    const timer = window.setTimeout(() => controller.abort(), options.timeoutMs || 120000);
    try {
      const response = await fetch(`${API_BASE}${path}`, { ...options, headers, signal: controller.signal });
      const type = response.headers.get("content-type") || "";
      const data = type.includes("json") ? await response.json().catch(() => null) : await response.text();
      if (!response.ok) throw new ApiError(errorMessage(data, `HTTP ${response.status}`), response.status, data);
      return { response, data };
    } catch (error) {
      if (error?.name === "AbortError") throw new ApiError("请求超时，请检查上游状态后再重试");
      throw error;
    } finally {
      window.clearTimeout(timer);
    }
  }

  async function requestBlob(path, options = {}, key = "") {
    if (key && !["image", "video"].some(kind => accountKeys.owns(kind, key))) throw new ApiError("登录状态或密钥已变化，请重新读取。");
    const headers = new Headers(options.headers || {});
    if (key) headers.set("Authorization", `Bearer ${key}`);
    const controller = new AbortController();
    const timer = window.setTimeout(() => controller.abort(), options.timeoutMs || 120000);
    try {
      const response = await fetch(`${API_BASE}${path}`, { ...options, headers, signal: controller.signal });
      if (!response.ok) {
        const text = await response.text().catch(() => "");
        throw new ApiError(errorMessage(text, `HTTP ${response.status}`), response.status, text);
      }
      return await response.blob();
    } catch (error) {
      if (error?.name === "AbortError") throw new ApiError("请求超时，请检查上游状态后再重试");
      throw error;
    } finally {
      window.clearTimeout(timer);
    }
  }

  function rpcId() { return `media-ui-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`; }

  async function mcpCall(profile, name, args, key) {
    const body = { jsonrpc: "2.0", id: rpcId(), method: "tools/call", params: { name, arguments: args } };
    const result = await request(`/mcp/${profile}`, { method: "POST", body: JSON.stringify(body) }, key);
    if (result.data?.error) throw new ApiError(result.data.error.message || "MCP 请求失败", 400, result.data);
    const toolResult = result.data?.result;
    if (toolResult?.isError) throw new ApiError(toolResult.content?.find((item) => item.type === "text")?.text || "MCP 工具失败", 400, result.data);
    return toolResult || {};
  }

  function setStatus(kind, message, type = "") {
    const node = $(`${kind}-status`);
    node.textContent = message || "";
    node.className = `form-status ${type}`.trim();
  }

  function setBusy(kind, busy) {
    state[kind].busy = busy;
    $(`${kind}-submit`).disabled = busy;
    $(`${kind}-submit`).classList.toggle("loading", busy);
  }

  function setOptions(select, values, selected = "") {
    select.replaceChildren();
    values.forEach((value) => {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = value;
      select.append(option);
    });
    if (selected && values.includes(selected)) select.value = selected;
  }

  function modelIds(data) {
    const rows = Array.isArray(data?.data) ? data.data : [];
    return rows.map((row) => String(row?.id || "").trim()).filter(Boolean);
  }

  function isImageModel(id) { return /image|imagine-image/i.test(id); }
  function isVideoModel(id) { return /seedance|seedace|doubao|kling|grok-imagine-video|grok-video/i.test(id); }

  async function loadModels(kind) {
    const key = selectedKey(kind);
    if (!key) {
      setStatus(kind, "请选择可用密钥；没有密钥时请前往 API Keys 创建。", "error");
      return;
    }
    state[kind].key = key;
    setStatus(kind, "正在读取模型…");
    try {
      const result = await request("/v1/models", {}, key);
      const all = modelIds(result.data);
      const filtered = all.filter(kind === "image" ? isImageModel : isVideoModel);
      state[kind].models = filtered.length ? filtered : all;
      if (!state[kind].models.length) throw new ApiError("该 API Key 没有可用模型");
      setOptions($(`${kind}-model`), state[kind].models);
      if (kind === "image") updateImageOptions();
      else updateVideoOptions();
      $(`${kind}-model-hint`).textContent = `已读取 ${state[kind].models.length} 个可用模型。`;
      setStatus(kind, "模型读取成功", "success");
    } catch (error) {
      state[kind].models = [];
      setOptions($(`${kind}-model`), kind === "image" ? IMAGE_FALLBACK : VIDEO_FALLBACK);
      if (kind === "image") updateImageOptions();
      else updateVideoOptions();
      $(`${kind}-model-hint`).textContent = "读取失败；下拉框仅保留示例模型，不代表该 Key 可用。";
      setStatus(kind, error.message || "模型读取失败", "error");
    }
  }

  async function restoreKeys() {
    try {
      await accountKeys.refresh();
      for (const kind of ["image", "video"]) {
        const choices = accountKeys.list(kind);
        const select = $(`${kind}-key`);
        select.replaceChildren();
        for (const choice of choices) {
          const option = document.createElement("option");
          option.value = choice.id;
          option.textContent = choice.name;
          select.append(option);
        }
        if (!choices.length) {
          setStatus(kind, "暂无可用密钥，请前往 API Keys 创建对应分组密钥。", "error");
        } else {
          if (kind === "image") updateImageOptions();
          await loadModels(kind);
        }
      }
      void resumeVideoTasks();
    } catch (error) {
      clearKeys();
      for (const kind of ["image", "video"]) setStatus(kind, error.message, "error");
    }
  }

  function initKeys() {
    ["image", "video"].forEach((kind) => {
      sessionStorage.removeItem(`meteor-media-${kind}-key`);
      setOptions($(`${kind}-model`), kind === "image" ? IMAGE_FALLBACK : VIDEO_FALLBACK);
    });
  }

  function clearKeys() {
    accountKeys.clear();
    ["image", "video"].forEach((kind) => {
      sessionStorage.removeItem(`meteor-media-${kind}-key`);
      state[kind].key = "";
      $(`${kind}-key`).replaceChildren();
      if (kind === "image") updateImageOptions();
      setStatus(kind, "本页密钥已清除");
    });
  }

  function dataUrl(row) {
    if (typeof row?.b64_json === "string" && row.b64_json) {
      if (row.b64_json.startsWith("data:")) return /^data:image\/(?:png|jpeg|webp|avif);/i.test(row.b64_json) ? row.b64_json : "";
      return `data:image/png;base64,${row.b64_json}`;
    }
    if (typeof row?.url === "string" && /^(?:https?:|blob:)/i.test(row.url)) return row.url;
    return "";
  }

  function emptyResult(kind, title) {
    const result = $(`${kind}-result`);
    result.replaceChildren();
    const empty = document.createElement("div");
    empty.className = "empty-state";
    const strong = document.createElement("strong");
    strong.textContent = title;
    empty.append(strong);
    result.append(empty);
  }

  function renderImageHistory(entries) {
    const result = $("image-result");
    result.replaceChildren();
    if (!entries.length) return emptyResult("image", "生成结果会显示在这里");
    entries.forEach((entry) => {
      const rows = Array.isArray(entry.images) ? entry.images : [];
      const card = document.createElement("div");
      card.className = "result-card";
      const head = document.createElement("div");
      head.className = "result-head";
      const title = document.createElement("strong");
      title.textContent = `${entry.model} · ${rows.length} 张结果`;
      const time = document.createElement("span");
      time.textContent = new Date(entry.createdAt).toLocaleString("zh-CN");
      head.append(title, time);
      const grid = document.createElement("div");
      grid.className = "image-grid";
      rows.forEach((row, index) => {
        const src = dataUrl(row);
        if (!src) return;
        const item = document.createElement("div");
        item.className = "image-item";
        const image = document.createElement("img");
        image.src = src;
        image.alt = row.revised_prompt || entry.prompt || `生成图片 ${index + 1}`;
        image.loading = "lazy";
        const tools = document.createElement("div");
        tools.className = "image-tools";
        const label = document.createElement("small");
        label.textContent = `图片 ${index + 1}`;
        const link = document.createElement("a");
        link.href = src;
        link.target = "_blank";
        link.rel = "noreferrer";
        link.download = `meteor-image-${index + 1}.png`;
        link.textContent = "打开 / 下载";
        tools.append(label, link);
        item.append(image, tools);
        grid.append(item);
      });
      if (!grid.children.length) {
        const empty = document.createElement("div");
        empty.className = "empty-state";
        empty.textContent = "接口已返回，但没有可显示的图片数据。";
        result.append(empty);
        return;
      }
      card.append(head, grid);
      if (rows.some((row) => row.revised_prompt)) {
        const revised = document.createElement("p");
        revised.className = "task-prompt";
        revised.textContent = rows.find((row) => row.revised_prompt)?.revised_prompt || "";
        card.append(revised);
      }
      result.append(card);
    });
  }

  async function restoreImageHistory() {
    const scope = mediaHistory.userScope();
    const epoch = ++state.image.restoreEpoch;
    if (state.image.scope !== scope) {
      mediaHistory.revokeObjectUrls(state.image.objectUrls);
      state.image.scope = scope;
      state.image.objectUrls = [];
      state.image.history = [];
      emptyResult("image", "生成结果会显示在这里");
    }
    try {
      const loaded = await mediaHistory.readImages(scope);
      if (!scope || mediaHistory.userScope() !== scope || epoch !== state.image.restoreEpoch) {
        mediaHistory.revokeObjectUrls(loaded.objectUrls);
        return;
      }
      mediaHistory.revokeObjectUrls(state.image.objectUrls);
      state.image.history = loaded.entries;
      state.image.objectUrls = loaded.objectUrls;
      renderImageHistory(state.image.history);
    } catch {
      if ((scope && mediaHistory.userScope() !== scope) || epoch !== state.image.restoreEpoch) return;
      mediaHistory.revokeObjectUrls(state.image.objectUrls);
      state.image.history = [];
      state.image.objectUrls = [];
      emptyResult("image", "生成结果会显示在这里");
    }
  }

  async function submitImage(event) {
    event.preventDefault();
    if (state.image.busy) return;
    const key = selectedKey("image");
    const model = $("image-model").value.trim();
    const prompt = $("image-prompt").value.trim();
    const n = Number($("image-count").value);
    const refs = [...$("image-reference").files];
    const scope = mediaHistory.userScope();
    if (!key) return setStatus("image", "请先读取并选择绘图密钥", "error");
    if (!scope) return setStatus("image", "登录状态无效，请重新登录", "error");
    if (!model || !prompt) return setStatus("image", "模型和提示词不能为空", "error");
    const size = MeteorImageOptions.resolveSize($("image-tier").value, $("image-orientation").value);
    if (!size) return setStatus("image", "当前密钥没有可用的图片档位", "error");
    if (refs.length > 4) return setStatus("image", "参考图最多 4 张", "error");
    if (refs.some((file) => file.size > 10 * 1024 * 1024)) return setStatus("image", "单张参考图不能超过 10 MB", "error");
    state.image.key = key;
    setBusy("image", true);
    setStatus("image", "正在生成图片…");
    try {
      let result;
      const payload = { model, prompt, n, response_format: "b64_json", size };
      const quality = $("image-quality").value;
      if (quality !== "auto") payload.quality = quality;
      if (refs.length) {
        const form = new FormData();
        Object.entries(payload).forEach(([name, value]) => form.append(name, String(value)));
        const field = refs.length === 1 ? "image" : "image[]";
        refs.forEach((file) => form.append(field, file, file.name));
        result = await request("/v1/images/edits", { method: "POST", body: form }, key);
      } else {
        result = await request("/v1/images/generations", { method: "POST", body: JSON.stringify(payload) }, key);
      }
      const entry = {
        id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        createdAt: Date.now(),
        model,
        prompt,
        images: Array.isArray(result.data?.data) ? result.data.data : [],
      };
      if (mediaHistory.userScope() !== scope) return;
      try {
        const saved = await mediaHistory.saveImages(entry, scope);
        if (mediaHistory.userScope() !== scope) return;
        if (saved) await restoreImageHistory();
        else {
          state.image.history = [entry, ...state.image.history].slice(0, mediaHistory.HISTORY_LIMIT);
          renderImageHistory(state.image.history);
        }
      } catch {
        if (mediaHistory.userScope() !== scope) return;
        state.image.history = [entry, ...state.image.history].slice(0, mediaHistory.HISTORY_LIMIT);
        renderImageHistory(state.image.history);
      }
      setStatus("image", "图片生成完成", "success");
    } catch (error) {
      setStatus("image", error.message || "图片生成失败", "error");
    } finally {
      setBusy("image", false);
    }
  }

  function videoFamily(model) {
    const id = model.toLowerCase();
    if (id === "kling-v3" || id === "kling-v3-omni") return "kling";
    if (id.includes("grok-imagine-video") || id === "grok-video") return "grok";
    return "seedance";
  }

  function updateVideoOptions() {
    const family = videoFamily($("video-model").value);
    const resolution = family === "kling" ? ["720p", "1080p", "4K"] : family === "grok" ? ["480p", "720p"] : ["480p", "720p"];
    const ratios = family === "kling" ? ["16:9", "9:16", "1:1"] : family === "grok" ? ["16:9", "9:16", "1:1", "4:3", "3:4"] : ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
    const currentResolution = $("video-resolution").value;
    const currentRatio = $("video-ratio").value;
    setOptions($("video-resolution"), resolution, currentResolution);
    setOptions($("video-ratio"), ratios, currentRatio);
    const min = family === "kling" || family === "grok" ? 3 : 4;
    $("video-duration").min = String(min);
    if (Number($("video-duration").value) < min) $("video-duration").value = String(min);
    $("video-duration-hint").textContent = `${family === "kling" || family === "grok" ? "当前模型" : "Seedance / Doubao"}允许 ${min}–15 秒。`;
    if (family === "kling" && $("video-mode").value === "text_with_reference") $("video-mode").value = "text_with_reference";
  }

  function parseLines(value) { return value.split("\n").map((line) => line.trim()).filter(Boolean); }

  async function uploadMaterial(file, key) {
    const result = await mcpCall("video", "create_material_upload", { file_name: file.name, content_type: file.type, size_bytes: file.size }, key);
    const info = result.structuredContent || {};
    if (!info.material_id || !info.upload_url) throw new ApiError("素材上传地址无效");
    const response = await fetch(info.upload_url, { method: info.method || "PUT", headers: info.headers || { "Content-Type": file.type }, body: file });
    if (!response.ok) throw new ApiError(`素材上传失败（HTTP ${response.status}）`, response.status);
    return info.material_id;
  }

  function taskIdFrom(data) {
    return String(data?.id || data?.task_id || data?.request_id || data?.data?.id || data?.data?.task_id || data?.data?.request_id || "").trim();
  }

  function safeRemoteUrl(value) {
    return typeof value === "string" && /^https?:\/\//i.test(value.trim()) ? value.trim() : "";
  }

  function videoInfo(data, fallbackId = "") {
    const candidates = [data, data?.data, data?.task, data?.data?.task].filter(Boolean);
    let status = "";
    let url = "";
    let reason = "";
    let id = fallbackId;
    for (const item of candidates) {
      if (!status && item.status) status = String(item.status).toLowerCase();
      if (!id) id = String(item.id || item.task_id || item.request_id || "").trim();
      url ||= safeRemoteUrl(item.result_url || item.video_url || item.url || item.video?.url || item.result?.url);
      reason ||= String(item.fail_reason || item.error?.message || item.message || "").trim();
    }
    if (["success", "succeeded", "completed", "complete", "done", "succeed"].includes(status) || url) status = "success";
    else if (["failure", "failed", "error", "cancelled", "canceled"].includes(status)) status = "failure";
    else status = "pending";
    return { id, status, url, reason };
  }

  async function statusRequest(taskId, key) {
    try {
      return (await request(`/v1/videos/generations/${encodeURIComponent(taskId)}`, {}, key)).data;
    } catch (error) {
      // The fallback keeps compatibility with clients that expose status only through the retained MCP contract.
      if (!(error instanceof ApiError) || error.status < 400) throw error;
      const tool = await mcpCall("video", "get_video", { task_id: taskId }, key);
      return tool.structuredContent || tool;
    }
  }

  async function fetchVideoContent(taskId, key) {
    try {
      const blob = await requestBlob(`/v1/videos/generations/${encodeURIComponent(taskId)}/content`, {}, key);
      if (blob.size && (blob.type.startsWith("video/") || blob.type === "application/octet-stream")) return URL.createObjectURL(blob);
    } catch { /* Some Sub2API versions return a provider URL instead. */ }
    return "";
  }

  function createVideoCard(info, prompt, model, createdAt) {
    const card = document.createElement("div");
    card.className = "result-card task-card";
    const head = document.createElement("div");
    head.className = "result-head";
    const title = document.createElement("strong");
    title.textContent = `${model} · 视频任务`;
    const time = document.createElement("span");
    time.textContent = new Date(createdAt || Date.now()).toLocaleString("zh-CN");
    head.append(title, time);
    const status = document.createElement("div");
    status.className = "task-status";
    const pill = document.createElement("span");
    pill.className = `status-pill ${info.status === "success" ? "success" : info.status === "failure" ? "error" : ""}`.trim();
    pill.textContent = info.status === "success" ? "已完成" : info.status === "failure" ? "失败" : "处理中";
    const id = document.createElement("span");
    id.className = "task-id";
    id.textContent = info.id ? `任务 ID：${info.id}` : "等待任务 ID…";
    status.append(pill, id);
    const promptNode = document.createElement("p");
    promptNode.className = "task-prompt";
    promptNode.textContent = prompt;
    card.append(head, status, promptNode);
    if (info.reason) {
      const reason = document.createElement("p");
      reason.className = "task-prompt";
      reason.textContent = info.reason;
      card.append(reason);
    }
    if (info.url) {
      const wrap = document.createElement("div");
      wrap.className = "video-wrap";
      const video = document.createElement("video");
      video.controls = true;
      video.preload = "metadata";
      video.src = info.url;
      wrap.append(video);
      const link = document.createElement("a");
      link.className = "video-link";
      link.href = info.url;
      link.target = "_blank";
      link.rel = "noreferrer";
      link.download = `meteor-video-${info.id || "generated"}.mp4`;
      link.textContent = "打开 / 下载视频";
      card.append(wrap, link);
    }
    return card;
  }

  function renderVideoHistory(records) {
    const result = $("video-result");
    result.replaceChildren();
    if (!records.length) return emptyResult("video", "任务结果会显示在这里");
    records.forEach((record) => result.append(createVideoCard(record, record.prompt, record.model, record.createdAt)));
  }

  function revokeVideoObjectUrls() {
    mediaHistory.revokeObjectUrls([...state.video.objectUrls.values()]);
    state.video.objectUrls.clear();
  }

  function rememberVideo(record, scope, epoch) {
    if (!scope || mediaHistory.userScope() !== scope || epoch !== state.video.pollEpoch) return false;
    if (record.url?.startsWith("blob:")) {
      const previous = state.video.objectUrls.get(record.id);
      if (previous && previous !== record.url) mediaHistory.revokeObjectUrls([previous]);
      state.video.objectUrls.set(record.id, record.url);
    } else if (record.url) {
      const previous = state.video.objectUrls.get(record.id);
      if (previous) mediaHistory.revokeObjectUrls([previous]);
      state.video.objectUrls.delete(record.id);
    }
    const persisted = mediaHistory.upsertVideo(record, scope);
    if (mediaHistory.userScope() !== scope || epoch !== state.video.pollEpoch) return false;
    state.video.history = persisted.map((item) => ({ ...item, url: state.video.objectUrls.get(item.id) || item.url }));
    renderVideoHistory(state.video.history);
    return true;
  }

  function restoreVideoHistory() {
    const scope = mediaHistory.userScope();
    if (state.video.scope !== scope) {
      state.video.pollEpoch += 1;
      revokeVideoObjectUrls();
      state.video.scope = scope;
    }
    state.video.history = mediaHistory.readVideos(scope).map((item) => ({ ...item, url: state.video.objectUrls.get(item.id) || item.url }));
    renderVideoHistory(state.video.history);
  }

  async function pollVideo(taskId, key, keyId, prompt, model, createdAt, scope, epoch) {
    const pollKey = `${scope}:${taskId}`;
    if (state.video.polling.has(pollKey)) return null;
    state.video.polling.add(pollKey);
    try {
      const deadline = createdAt + mediaHistory.PENDING_TTL_MS;
      do {
        if (mediaHistory.userScope() !== scope || epoch !== state.video.pollEpoch) return null;
        const data = await statusRequest(taskId, key);
        if (mediaHistory.userScope() !== scope || epoch !== state.video.pollEpoch) return null;
        const info = videoInfo(data, taskId);
        if (info.status === "success" && !info.url) info.url = await fetchVideoContent(taskId, key);
        if (mediaHistory.userScope() !== scope || epoch !== state.video.pollEpoch) {
          if (info.url?.startsWith("blob:")) mediaHistory.revokeObjectUrls([info.url]);
          return null;
        }
        if (!rememberVideo({ ...info, keyId, prompt, model, createdAt }, scope, epoch)) {
          if (info.url?.startsWith("blob:")) mediaHistory.revokeObjectUrls([info.url]);
          return null;
        }
        if (info.status === "success") return info;
        if (info.status === "failure") throw new ApiError(info.reason || "视频任务失败");
        if (Date.now() >= deadline) return info;
        await sleep(5000);
      } while (true);
    } finally {
      state.video.polling.delete(pollKey);
    }
  }

  async function resumeVideoTasks() {
    const scope = mediaHistory.userScope();
    const epoch = state.video.pollEpoch;
    if (!scope) return;
    for (const record of state.video.history) {
      const key = accountKeys.get("video", record.keyId);
      if (!key) continue;
      if (record.status === "pending" || record.status === "success") {
        void pollVideo(record.id, key, record.keyId, record.prompt, record.model, record.createdAt, scope, epoch).catch((error) => {
          setStatus("video", error.message || "视频任务状态读取失败", "error");
        });
      }
    }
  }

  async function submitVideo(event) {
    event.preventDefault();
    if (state.video.busy) return;
    const key = selectedKey("video");
    const model = $("video-model").value.trim();
    const prompt = $("video-prompt").value.trim();
    const duration = Number($("video-duration").value);
    const referenceUrls = parseLines($("video-reference-urls").value);
    const generalFiles = [...$("video-reference-files").files];
    const startFile = $("video-start-file").files[0];
    const endFile = $("video-end-file").files[0];
    const startUrl = $("video-start-url").value.trim();
    const endUrl = $("video-end-url").value.trim();
    const scope = mediaHistory.userScope();
    const epoch = state.video.pollEpoch;
    const keyId = $("video-key").value;
    const family = videoFamily(model);
    const min = family === "kling" || family === "grok" ? 3 : 4;
    if (!key) return setStatus("video", "请先读取并选择视频密钥", "error");
    if (!scope) return setStatus("video", "登录状态无效，请重新登录", "error");
    if (!model || !prompt) return setStatus("video", "模型和提示词不能为空", "error");
    if (prompt.length > 1300) return setStatus("video", "提示词不能超过 1,300 个字符", "error");
    if (!Number.isInteger(duration) || duration < min || duration > 15) return setStatus("video", `当前模型时长必须为 ${min}–15 秒`, "error");
    if (generalFiles.length > 9) return setStatus("video", "参考素材最多 9 个", "error");
    if ([...generalFiles, startFile, endFile].filter(Boolean).some((file) => file.size > 10 * 1024 * 1024)) return setStatus("video", "单个本地素材不能超过 10 MB", "error");
    state.video.key = key;
    setBusy("video", true);
    setStatus("video", "正在提交视频任务…");
    try {
      const resolution = $("video-resolution").value;
      const aspectRatio = $("video-ratio").value;
      const mode = $("video-mode").value;
      const audio = $("video-audio").checked;
      const payload = { model, prompt, duration, resolution, aspect_ratio: aspectRatio, mode, audio };
      let data;
      const materialIds = [];
      if (generalFiles.length || startFile || endFile) {
        for (const file of generalFiles) {
          setStatus("video", `正在上传本地参考素材（${materialIds.length + 1}）…`);
          materialIds.push(await uploadMaterial(file, key));
        }
        let startMaterialId = "";
        let endMaterialId = "";
        if (startFile) { setStatus("video", "正在上传首帧素材…"); startMaterialId = await uploadMaterial(startFile, key); }
        if (endFile) { setStatus("video", "正在上传尾帧素材…"); endMaterialId = await uploadMaterial(endFile, key); }
        const args = { ...payload, reference_images: referenceUrls, reference_material_ids: materialIds };
        if (startMaterialId) args.start_material_id = startMaterialId;
        else if (startUrl) args.start_image_url = startUrl;
        if (endMaterialId) args.end_material_id = endMaterialId;
        else if (endUrl) args.end_image_url = endUrl;
        const tool = await mcpCall("video", "create_video", args, key);
        data = tool.structuredContent || tool;
      } else {
        if (referenceUrls.length) payload.reference_images = referenceUrls;
        if (startUrl) payload.start_image_url = startUrl;
        if (endUrl) payload.end_image_url = endUrl;
        data = (await request("/v1/videos/generations", { method: "POST", body: JSON.stringify(payload) }, key)).data;
      }
      const taskId = taskIdFrom(data);
      if (!taskId) throw new ApiError("接口已响应，但没有返回任务 ID");
      if (mediaHistory.userScope() !== scope || epoch !== state.video.pollEpoch) return;
      state.video.taskId = taskId;
      const createdAt = Date.now();
      rememberVideo({ ...videoInfo(data, taskId), keyId, prompt, model, createdAt }, scope, epoch);
      setStatus("video", `任务已提交：${taskId}`, "success");
      const final = await pollVideo(taskId, key, keyId, prompt, model, createdAt, scope, epoch);
      if (final?.status === "success") setStatus("video", "视频生成完成", "success");
      else if (final?.status === "pending") setStatus("video", "任务仍在处理中，稍后返回本页可继续查询");
    } catch (error) {
      setStatus("video", error.message || "视频任务失败", "error");
    } finally {
      setBusy("video", false);
    }
  }

  function bindTabs() {
    const show = (target) => {
      const selected = ["image", "video", "docs"].includes(target) ? target : "image";
      document.querySelectorAll(".tab").forEach((tab) => tab.classList.toggle("active", tab.dataset.tab === selected));
      document.querySelectorAll("[data-panel]").forEach((panel) => panel.classList.toggle("hidden", panel.dataset.panel !== selected));
    };
    document.querySelectorAll(".tab").forEach((tab) => tab.addEventListener("click", () => {
      const target = tab.dataset.tab;
      show(target);
      if (window.history.replaceState) window.history.replaceState(null, "", `${window.location.pathname}#${target}`);
    }));
    window.addEventListener("hashchange", () => show(window.location.hash.slice(1)));
    show(window.location.hash.slice(1));
  }

  function bindCounters() {
    [["image-prompt", "image-prompt-count", 32000], ["video-prompt", "video-prompt-count", 1300]].forEach(([input, counter, max]) => {
      const update = () => { $(counter).textContent = `${$(input).value.length.toLocaleString("zh-CN")} / ${max.toLocaleString("zh-CN")}`; };
      $(input).addEventListener("input", update);
      update();
    });
  }

  function init() {
    if (new URLSearchParams(window.location.search).get("embedded") === "1") {
      document.body.classList.add("embedded");
      // Same-origin native Markdown wrapper: scope layout fixes to our frames.
      // No token-bearing external-menu URL or official bundle modification.
      try {
        const parentDocument = window.parent.document;
        if (window.parent !== window && !parentDocument.getElementById("meteor-media-layout")) {
          const style = parentDocument.createElement("style");
          style.id = "meteor-media-layout";
          const scope = '.custom-page-layout:has(iframe[src^="/media/?embedded=1#"])';
          style.textContent = `${scope} .toc-sidebar, ${scope} .toc-toggle-btn {display:none!important}
            ${scope} .markdown-page-content {padding:0!important;overflow:hidden!important}
            ${scope} .markdown-page-content > p {margin:0!important}
            ${scope} iframe {display:block;width:100%;height:calc(100vh - 150px)!important;min-height:450px!important}`;
          parentDocument.head.append(style);
        }
      } catch { /* A cross-origin host cannot be styled. */ }
    }
    $("endpoint-text").textContent = API_BASE.replace(/^https?:\/\//, "");
    initKeys();
    bindTabs();
    bindCounters();
    $("image-tier").addEventListener("change", updateImageOrientations);
    $("video-model").addEventListener("change", updateVideoOptions);
    $("image-load-models").addEventListener("click", () => void loadModels("image"));
    $("video-load-models").addEventListener("click", () => void loadModels("video"));
    $("image-form").addEventListener("submit", (event) => void submitImage(event));
    $("video-form").addEventListener("submit", (event) => void submitVideo(event));
    $("image-clear-history").addEventListener("click", () => {
      void mediaHistory.clearImages().then(() => restoreImageHistory());
    });
    $("video-clear-history").addEventListener("click", () => {
      const scope = mediaHistory.userScope();
      state.video.pollEpoch += 1;
      revokeVideoObjectUrls();
      mediaHistory.clearVideos(scope);
      state.video.history = [];
      renderVideoHistory(state.video.history);
    });
    $("clear-keys").addEventListener("click", () => { clearKeys(); void restoreKeys(); });
    for (const kind of ["image", "video"]) $(`${kind}-key`).addEventListener("change", () => {
      if (kind === "image") updateImageOptions();
      void loadModels(kind);
    });
    window.addEventListener("storage", event => {
      if (event.key === "auth_token" || event.key === "auth_user" || event.key === null) {
        clearKeys();
        setBusy("image", false);
        setBusy("video", false);
        void restoreImageHistory();
        restoreVideoHistory();
        void restoreKeys();
      }
    });
    updateImageOptions();
    updateVideoOptions();
    void restoreImageHistory();
    restoreVideoHistory();
    // Only authenticated key/model reads; generation always requires submission.
    void restoreKeys();
  }

  window.addEventListener("DOMContentLoaded", init);
})();
