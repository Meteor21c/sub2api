/* Display and history metadata for automatic Seedance reference preparation. */
(() => {
  "use strict";

  const STAGES = new Set([
    "asset_pending", "asset_reviewing", "video_submitting", "video_generating",
    "submission_unknown", "completed", "failed",
  ]);

  function stage(value) {
    const normalized = typeof value === "string" ? value.trim().toLowerCase() : "";
    return STAGES.has(normalized) ? normalized : "";
  }

  function progress(value) {
    if (!value || typeof value !== "object" || Array.isArray(value)) return null;
    const fields = ["total", "active", "processing", "failed"];
    const result = {};
    for (const field of fields) {
      const count = value[field] ?? (field === "total" ? null : 0);
      if (!Number.isInteger(count) || count < 0 || count > 100) return null;
      result[field] = count;
    }
    return result.active + result.processing + result.failed <= result.total ? result : null;
  }

  function read(value) {
    const candidates = [value];
    const seen = new Set();
    let assetStage = "";
    let assetProgress = null;
    for (let index = 0; index < candidates.length && index < 16; index += 1) {
      const item = candidates[index];
      if (!item || typeof item !== "object" || seen.has(item)) continue;
      seen.add(item);
      assetStage ||= stage(item.assetStage ?? item.stage);
      assetProgress ||= progress(item.assetProgress ?? item.asset_progress);
      for (const key of ["data", "raw", "task", "structuredContent"]) {
        if (item[key] && typeof item[key] === "object") candidates.push(item[key]);
      }
    }
    return {
      ...(assetStage ? { assetStage } : {}),
      ...(assetProgress ? { assetProgress } : {}),
    };
  }

  function taskStatus(value, url = "", assetStage = "") {
    if (stage(assetStage) === "submission_unknown") return "pending";
    const normalized = String(value || "").toLowerCase();
    if (["success", "succeeded", "completed", "complete", "done", "succeed"].includes(normalized) || url) return "success";
    if (["failure", "failed", "error", "cancelled", "canceled"].includes(normalized) || stage(assetStage) === "failed") return "failure";
    return "pending";
  }

  function describe(info = {}) {
    if (info.status === "success") return { label: "已完成", detail: "" };
    const currentStage = stage(info.assetStage);
    if (info.status === "failure") return {
      label: "失败",
      detail: currentStage === "failed" ? "任务未完成，预扣费用将退回；到账以账户余额为准。" : "",
    };
    const ready = progress(info.assetProgress);
    const count = ready?.total ? `（已就绪 ${ready.active}/${ready.total}）` : "";
    switch (currentStage) {
      case "asset_pending": return { label: "素材入库中", detail: `正在准备参考素材${count}，完成审核后自动生成。` };
      case "asset_reviewing": return { label: "素材审核中", detail: `参考素材正在审核${count}；通过后自动生成，未通过会退回预扣。` };
      case "video_submitting": return { label: "提交生成中", detail: "素材已就绪，正在提交视频生成。" };
      case "video_generating": return { label: "视频生成中", detail: "视频正在生成，请稍候。" };
      case "submission_unknown": return { label: "提交待确认", detail: "上游提交结果待确认，请勿重复提交。" };
      case "completed": return { label: "结果同步中", detail: "正在读取视频结果。" };
      default: return { label: "处理中", detail: "" };
    }
  }

  function pollInterval(info = {}) {
    const currentStage = stage(info.assetStage);
    if (currentStage === "submission_unknown") return 30000;
    if (currentStage === "asset_pending" || currentStage === "asset_reviewing") return 10000;
    return 5000;
  }

  globalThis.MeteorVideoAssetStatus = { stage, progress, read, taskStatus, describe, pollInterval };
})();
