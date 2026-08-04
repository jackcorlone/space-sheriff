const $ = (id) => document.getElementById(id);
const queryToken = new URLSearchParams(location.search).get("token") || "";
const SESSION_TOKEN =
  queryToken || sessionStorage.getItem("space-sheriff-token") || "";
if (queryToken) {
  sessionStorage.setItem("space-sheriff-token", queryToken);
  history.replaceState(null, "", location.pathname);
}

let roots = [];
let pollTimer = null;
let allRecords = [];
let selected = new Set();
let duplicateGroups = [];
let plan = new Set();
let persistedPlanRecords = new Map();
let planSave = Promise.resolve();
let policyState = {activeId: "", policies: []};
let schedulesState = {backend: "", schedules: [], recent: []};
let currentFolder = "";
let scanRoot = "";
let latestStatus = {state: "idle", phase: "idle", filesSeen: 0, bytesSeen: 0, errors: 0, excluded: 0};
let backendAvailable = false;

const FILE_PAGE_MESSAGE = "当前打开的是静态 file:// 页面，尚未连接本地分析引擎。请在项目的 portable 目录运行 go run .，然后打开终端输出的 http://127.0.0.1:端口/?token=... 地址。文件分析始终在本机完成，不使用云端服务。";
const BACKEND_MESSAGE = "无法连接本地分析引擎。请确认 Space Sheriff 已通过 go run . 或已构建的可执行文件启动，并使用它自动打开的本地地址。";
const SESSION_MESSAGE = "本地分析引擎已运行，但当前页面没有有效会话令牌。请使用程序自动打开的地址，不要直接打开 portable/web/index.html。";

const ICON_PATHS = {
  shield: '<path d="M12 2 19 5v5c0 4.7-2.8 8.6-7 10-4.2-1.4-7-5.3-7-10V5l7-3Z"/><path d="m9 12 2 2 4-4"/>',
  power: '<path d="M12 3v9"/><path d="M18.4 6.6a8 8 0 1 1-12.8 0"/>',
  scan: '<path d="M4 7V5a1 1 0 0 1 1-1h2M17 4h2a1 1 0 0 1 1 1v2M20 17v2a1 1 0 0 1-1 1h-2M7 20H5a1 1 0 0 1-1-1v-2"/><circle cx="12" cy="12" r="3"/>',
  stop: '<rect x="6" y="6" width="12" height="12" rx="2"/>',
  calendar: '<rect x="3" y="4" width="18" height="17" rx="2"/><path d="M16 2v4M8 2v4M3 10h18"/>',
  check: '<path d="m5 12 4 4L19 6"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  refresh: '<path d="M20 11a8 8 0 0 0-14.8-3L3 11"/><path d="M3 5v6h6M4 13a8 8 0 0 0 14.8 3L21 13"/><path d="M21 19v-6h-6"/>',
  upload: '<path d="M12 16V4M8 8l4-4 4 4"/><path d="M5 13v5a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-5"/>',
  download: '<path d="M12 4v12M8 12l4 4 4-4"/><path d="M5 20h14"/>',
  edit: '<path d="m4 16-.8 4.8L8 20l11-11a2.8 2.8 0 0 0-4-4L4 16Z"/><path d="m13.5 6.5 4 4"/>',
  database: '<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v7c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 12v7c0 1.7 3.6 3 8 3s8-1.3 8-3v-7"/>',
  spark: '<path d="m12 3 1.5 5.5L19 10l-5.5 1.5L12 17l-1.5-5.5L5 10l5.5-1.5L12 3Z"/><path d="m19 16 .6 2.4L22 19l-2.4.6L19 22l-.6-2.4L16 19l2.4-.6L19 16Z"/>',
  gauge: '<path d="M4.5 16a8 8 0 1 1 15 0"/><path d="m12 12 4-4M6 19h12"/>',
  file: '<path d="M6 3h8l4 4v14H6z"/><path d="M14 3v5h5M9 13h6M9 17h6"/>',
  folder: '<path d="M3 6a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
  copy: '<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>',
  clipboard: '<rect x="5" y="4" width="14" height="17" rx="2"/><path d="M9 4V3h6v1M9 10h6M9 14h6M9 18h3"/>',
  trash: '<path d="M4 7h16M10 11v6M14 11v6M6 7l1 14h10l1-14M9 7V4h6v3"/>',
  clear: '<path d="m6 6 12 12M18 6 6 18"/>',
  arrowUp: '<path d="m5 10 7-7 7 7M12 3v14"/><path d="M5 21h14"/>',
  chart: '<path d="M4 19V5M4 19h16"/><path d="m7 15 3-4 3 2 4-6"/>',
  activity: '<path d="M3 12h4l2-6 4 12 2-6h6"/>',
  alert: '<path d="m12 3 9 17H3z"/><path d="M12 9v4M12 17h.01"/>'
};

function iconSVG(name) {
  return `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICON_PATHS[name] || ICON_PATHS.spark}</svg>`;
}

function hydrateIcons() {
  document.querySelectorAll("[data-icon]").forEach((node) => {
    node.setAttribute("aria-hidden", "true");
    node.innerHTML = iconSVG(node.dataset.icon);
  });
}

const humanSize = (bytes) => {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = Number(bytes), unit = 0;
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
  return `${unit ? value.toFixed(1) : Math.round(value)} ${units[unit]}`;
};

const escapeHTML = (value) => String(value).replace(
  /[&<>"']/g,
  (char) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char])
);

const dashboardStateLabel = {
  idle: "准备就绪", running: "正在扫描", done: "扫描完成",
  cancelled: "已停止", budget_exceeded: "达到预算", error: "扫描失败"
};

const errorCategoryLabel = {
  walk: "目录访问",
  metadata: "文件元数据",
  index: "本地索引",
  fingerprint: "快速指纹",
  hash: "完整哈希",
  unknown: "其他"
};

const recordNextSteps = {
  SYSTEM_CORE: "不要删除；这是核心系统路径。",
  SYSTEM_PROTECTED: "不要删除；这是系统或应用保护路径。",
  PERSONAL_FILE: "先打开确认并备份，再决定是否处理。",
  CACHE_OLD: "确认相关应用已关闭后，可考虑移入回收站。",
  CACHE_RECENT: "先保留；文件较新，过一段时间再复查。",
  INSTALLER_OLD: "确认软件已经安装，再考虑移入回收站。",
  ARCHIVE_OLD: "确认压缩包已有副本或已解压，再决定。",
  LARGE_STALE: "先确认用途并备份，不要只按大小删除。",
  UNKNOWN: "暂时保留，先确认它的用途。"
};

function nextStepForRecord(record) {
  return recordNextSteps[record?.advice?.ruleId] || "先确认用途，再决定是否处理。";
}

function guidanceForStatus(status = latestStatus) {
  const current = status || {};
  const files = Number(current.filesSeen || 0);
  const state = current.state || "idle";
  if (state === "error") {
    return [
      {tone: "danger", title: "扫描没有完成", body: current.message || "请检查扫描位置、权限和本地数据库后重试。"},
      {title: "换小范围重试", body: "先扫描当前用户目录，确认本地后端可以正常读取文件。"},
      {title: "确认错误信息", body: "如果仍然失败，保留错误提示和扫描路径，便于进一步定位。"}
    ];
  }
  if (!files && state !== "running") {
    return [
      {title: "从用户目录开始", body: "建议先扫描 /Users/你的用户名，系统目录更容易产生权限错误。"},
      {title: "等扫描完成", body: "看到“扫描完成”或“达到预算”后，再查看大文件结果。"},
      {title: "阅读建议和原因", body: "先看 advice、reason 和 nextStep，不要只看文件大小。"},
      {title: "最后决定动作", body: "确认用途、备份和重复组后，才加入清理计划。"}
    ];
  }
  const steps = [];
  if (current.errors) {
    steps.push({tone: "warn", title: "先处理读取错误", body: "缩小扫描范围到用户目录；如果必须扫描系统目录，再检查系统权限。"});
  } else {
    steps.push({title: "先看建议标签", body: "优先阅读大文件列表中的 reason 和 nextStep，建议只是辅助判断。"});
  }
  if (current.excluded) {
    steps.push({title: "把排除当作提示", body: `已有 ${Number(current.excluded).toLocaleString()} 项按规则跳过，它们不是读取错误。`});
  }
  if (current.budgetExceeded || state === "budget_exceeded") {
    steps.push({tone: "warn", title: "扫描达到预算", body: "提高字节/时间上限，或把大目录拆成几个用户目录分开扫描。"});
  }
  if (allRecords.length) {
    steps.push({title: "从最大的结果开始", body: "按大小排序后，结合位置、修改时间和原因逐项复核。"});
  } else {
    steps.push({title: "暂时没有大文件结果", body: "可以降低最小文件阈值后重新扫描，但不要为了结果关闭系统保护。"});
  }
  if (duplicateGroups.length) {
    steps.push({title: "重复组只保留一个", body: "相同 duplicateGroup 的文件内容相同；确认要保留的那一个后再处理其余副本。"});
  }
  if (plan.size) {
    steps.push({title: "清理前再确认计划", body: `当前计划有 ${plan.size.toLocaleString()} 个文件；移入回收站后仍可从回收站恢复。`});
  }
  return steps.slice(0, 4);
}

function renderAnalysisGuide(status = latestStatus) {
  const current = status || {};
  const steps = guidanceForStatus(current);
  $("analysisGuide").innerHTML = steps.map((item) => `
    <li class="guide-item ${item.tone || ""}"><div><strong>${escapeHTML(item.title)}</strong><span>${escapeHTML(item.body)}</span></div></li>`
  ).join("");
  $("analysisGuideState").textContent = dashboardStateLabel[current.state] || "待开始";
  $("analysisGuideNote").textContent = current.errors
    ? "指南优先帮助你解释错误；它不会自动修改任何文件。"
    : "每一步都是解释性提示，最终是否清理仍由你确认。";
}

function reportSummary(status = latestStatus) {
  const current = {...latestStatus, ...(status || {})};
  return {
    state: current.state || "idle",
    phase: current.phase || "",
    root: current.root || scanRoot,
    filesSeen: Number(current.filesSeen || 0),
    bytesSeen: Number(current.bytesSeen || 0),
    errors: Number(current.errors || 0),
    excluded: Number(current.excluded || 0),
    filesFingerprinted: Number(current.filesFingerprinted || 0),
    filesHashed: Number(current.filesHashed || 0),
    hashesReused: Number(current.hashesReused || 0),
    errorCounts: {...(current.errorCounts || {})},
    errorSamples: Array.isArray(current.errorSamples) ? current.errorSamples.slice() : [],
    budgetBytes: Number(current.budgetBytes || 0),
    budgetDurationMs: Number(current.budgetDurationMs || 0),
    budgetExceeded: current.budgetExceeded || "",
    resultCount: allRecords.length,
    duplicateGroupCount: duplicateGroups.length,
    plannedCount: plan.size,
    guidance: guidanceForStatus(current).map((item) => ({title: item.title, action: item.body}))
  };
}

function renderDashboard(status = latestStatus) {
  latestStatus = {...latestStatus, ...(status || {})};
  const current = latestStatus;
  const files = Number(current.filesSeen || 0);
  const bytes = Number(current.bytesSeen || 0);
  const duplicateBytes = duplicateGroups.reduce((sum, group) => sum + Number(group.reclaimable || 0), 0);
  const duplicateFiles = duplicateGroups.reduce((sum, group) => sum + group.files.length, 0);
  const plannedBytes = planRecords().reduce((sum, record) => sum + Number(record.size || 0), 0);
  const resultBytes = allRecords.reduce((sum, record) => sum + Number(record.size || 0), 0);
  const state = dashboardStateLabel[current.state] || current.state || "准备就绪";
  $("dashboardState").textContent = state;
  $("dashboardRoot").textContent = current.root || "选择位置开始分析";
  $("dashboardFiles").textContent = files ? files.toLocaleString() : "—";
  $("dashboardBytes").textContent = files ? `${humanSize(bytes)} · 规则排除 ${(current.excluded || 0).toLocaleString()} 项` : "尚未开始扫描";
  $("dashboardDuplicates").textContent = duplicateBytes ? humanSize(duplicateBytes) : "—";
  $("dashboardDuplicateMeta").textContent = duplicateFiles ? `${duplicateGroups.length} 组 · ${duplicateFiles.toLocaleString()} 个文件` : "等待内容分析";
  $("dashboardPlan").textContent = plannedBytes ? humanSize(plannedBytes) : "—";
  $("dashboardPlanMeta").textContent = plan.size ? `${plan.size.toLocaleString()} 个文件待复核` : "尚未选择文件";
  $("dashboardChartMeta").textContent = files ? `${humanSize(bytes)} 已检查` : "未扫描";
  const chartItems = [
    ["扫描空间", bytes, "逻辑字节"],
    ["大文件结果", resultBytes, `${allRecords.length.toLocaleString()} 个结果`],
    ["重复可回收", duplicateBytes, `${duplicateGroups.length.toLocaleString()} 组`],
    ["清理计划", plannedBytes, `${plan.size.toLocaleString()} 个文件`]
  ];
  const chartMax = Math.max(1, ...chartItems.map((item) => item[1]));
  $("spaceChart").setAttribute("aria-label", files ? "扫描空间指标图" : "空间画像尚未生成");
  $("spaceChart").innerHTML = files ? chartItems.map(([label, value, meta]) => `
    <div class="chart-row">
      <div class="chart-label"><span>${escapeHTML(label)} · ${escapeHTML(meta)}</span><strong>${humanSize(value)}</strong></div>
      <div class="chart-track"><i style="width:${Math.min(100, value / chartMax * 100).toFixed(1)}%"></i></div>
    </div>`).join("") : '<div class="chart-empty"><span class="ui-icon" data-icon="chart"></span>完成一次扫描后生成空间画像</div>';
  if (!files) hydrateIcons();
  const healthItems = [
    ["快速指纹", current.filesFingerprinted ? `${Number(current.filesFingerprinted).toLocaleString()} 个` : "未开始", current.filesFingerprinted ? "good" : ""],
    ["完整哈希", current.filesHashed || current.hashesReused ? `${Number(current.filesHashed || 0).toLocaleString()} 新 · ${Number(current.hashesReused || 0).toLocaleString()} 复用` : "未开始", current.filesHashed || current.hashesReused ? "good" : ""],
    ["读取错误", `${Number(current.errors || 0).toLocaleString()} 个`, current.errors ? "bad" : "good"],
    ["规则排除", `${Number(current.excluded || 0).toLocaleString()} 项`, ""],
    ["资源预算", current.budgetExceeded || (current.budgetBytes || current.budgetDurationMs) ? (current.budgetExceeded ? "已触发" : "已配置") : "未设置（不限）", current.budgetExceeded ? "warn" : ""]
  ];
  $("dashboardHealth").innerHTML = healthItems.map(([label, value, tone]) =>
    `<div class="health-item ${tone}"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value)}</strong></div>`
  ).join("");
  const healthState = current.errors ? "需留意读取错误" : current.state === "done" ? "数据完整" : state;
  $("dashboardHealthState").textContent = healthState;
  $("dashboardHealthNote").textContent = current.errors
    ? `读取错误通常来自权限受限的系统目录、文件在扫描期间消失或内容无法读取；${current.excluded ? `另有 ${Number(current.excluded).toLocaleString()} 项按规则跳过，它们不是错误。` : ""}建议先扫描用户目录。`
    : current.excluded
      ? `已按排除规则跳过 ${Number(current.excluded).toLocaleString()} 项，它们不是错误。`
      : "未发现读取错误或规则排除项。";
  const errorDetails = $("scanErrorDetails");
  const errorCounts = Object.entries(current.errorCounts || {}).filter(([, count]) => Number(count) > 0);
  const errorSamples = Array.isArray(current.errorSamples) ? current.errorSamples : [];
  errorDetails.classList.toggle("hidden", !current.errors);
  if (current.errors) {
    $("scanErrorCounts").innerHTML = errorCounts.length
      ? errorCounts.map(([category, count]) => "<span>" + escapeHTML(errorCategoryLabel[category] || category) + "：" + Number(count).toLocaleString() + "</span>").join("")
      : "<span>暂时没有分类计数。</span>";
    $("scanErrorSamples").innerHTML = errorSamples.length
      ? errorSamples.map((sample) => "<li><strong>" + escapeHTML(errorCategoryLabel[sample.category] || sample.category || "其他") + "</strong><span>" + escapeHTML(sample.message || "未知错误") + "</span>" + (sample.path ? "<code>" + escapeHTML(sample.path) + "</code>" : "") + "</li>").join("")
      : "<li>没有可展示的错误样本。</li>";
  } else {
    $("scanErrorCounts").innerHTML = "";
    $("scanErrorSamples").innerHTML = "";
  }
  renderAnalysisGuide(current);
}

hydrateIcons();
renderDashboard();
setBackendConnection(false, location.protocol === "file:" ? FILE_PAGE_MESSAGE : "正在连接本地分析引擎…");

function setBackendConnection(available, message = "") {
  backendAvailable = available;
  const notice = $("backendNotice");
  if (notice) {
    notice.classList.toggle("hidden", available);
    $("backendNoticeTitle").textContent = available ? "本地分析引擎已连接" : "未连接本地分析引擎";
    $("backendNoticeText").textContent = message || (available ? "" : BACKEND_MESSAGE);
  }
  const running = $("pulse")?.classList.contains("active") || false;
  setRunning(running);
}

async function api(path, options = {}) {
  if (location.protocol === "file:") {
    setBackendConnection(false, FILE_PAGE_MESSAGE);
    throw new Error(FILE_PAGE_MESSAGE);
  }
  try {
    const headers = {...(options.headers || {}), "X-Space-Sheriff-Token": SESSION_TOKEN};
    if (options.body) headers["Content-Type"] = "application/json";
    const response = await fetch(path, {...options, headers});
    if (!response.ok) {
      const detail = (await response.text()).trim() || `请求失败 (${response.status})`;
      if (response.status === 401 || response.status === 403) {
        setBackendConnection(false, SESSION_MESSAGE);
      }
      throw new Error(detail);
    }
    const result = await response.json();
    setBackendConnection(true);
    return result;
  } catch (error) {
    if (error instanceof TypeError) {
      setBackendConnection(false, BACKEND_MESSAGE);
      throw new Error(BACKEND_MESSAGE);
    }
    throw error;
  }
}

function toast(message) {
  $("toast").textContent = message;
  $("toast").classList.add("show");
  setTimeout(() => $("toast").classList.remove("show"), 3500);
}

function showDisk(root) {
  if (!root || !root.total) { $("disk").classList.add("hidden"); return; }
  $("disk").classList.remove("hidden");
  $("diskTotal").textContent = humanSize(root.total);
  $("diskFree").textContent = humanSize(root.free);
  $("diskUsed").style.width = `${Math.max(0, Math.min(100, 100 - root.free / root.total * 100))}%`;
}

async function loadRoots() {
  roots = await api("/api/roots");
  $("roots").innerHTML = roots.map(
    (root, index) => `<option value="${index}">${escapeHTML(root.label)}</option>`
  ).join("");
  if (roots.length) {
    $("root").value = roots[0].path;
    if (!$("scheduleRoot").value) $("scheduleRoot").value = roots[0].path;
    showDisk(roots[0]);
  }
}

async function refreshDisk() {
  roots = await api("/api/roots");
  showDisk(roots.find((item) => item.path === scanRoot) || roots[0]);
}

function setRunning(running) {
  $("scan").disabled = running || !backendAvailable;
  $("cancel").disabled = !running || !backendAvailable;
  $("pulse").classList.toggle("active", running);
  $("trashSelected").disabled = running || !backendAvailable || selected.size === 0;
  $("executePlan").disabled = running || !backendAvailable || plan.size === 0;
  $("checkpointDatabase").disabled = running || !backendAvailable;
  $("optimizeDatabase").disabled = running || !backendAvailable;
}

function policyByID(id) {
  return policyState.policies.find((policy) => policy.id === id);
}

function renderPolicy(policy) {
  if (!policy) {
    $("policyDescription").textContent = "没有可用策略";
    $("policyThresholds").innerHTML = "";
    return;
  }
  $("policyDescription").textContent =
    `${policy.description} · ${policy.builtIn ? "内置策略" : "自定义策略"} · v${policy.version}`;
  $("policyThresholds").innerHTML = [
    ["缓存", `${policy.cacheMinAgeDays} 天`],
    ["高置信缓存", `${policy.cacheHighConfidenceDays} 天`],
    ["安装包", `${policy.installerMinAgeDays} 天`],
    ["压缩包", `${policy.archiveMinAgeDays} 天`],
    ["陈旧大文件", `${humanSize(policy.largeStaleMinBytes)} / ${policy.largeStaleMinAgeDays} 天`]
  ].map(([label, value]) =>
    `<div class="metric"><strong>${escapeHTML(value)}</strong><span>${escapeHTML(label)}</span></div>`
  ).join("");
  $("activatePolicy").disabled = policy.id === policyState.activeId;
}

function renderPolicies(state) {
  policyState = state;
  $("policySelect").innerHTML = state.policies.map((policy) =>
    `<option value="${escapeHTML(policy.id)}" ${policy.id === state.activeId ? "selected" : ""}>${escapeHTML(policy.name)} · v${policy.version}${policy.builtIn ? "" : " · 自定义"}</option>`
  ).join("");
  renderPolicy(policyByID($("policySelect").value));
}

function renderHealth(health) {
  const integrity = health.integrity === "ok" ? "正常" : health.integrity;
  $("databaseHealth").innerHTML = [
    ["完整性", integrity],
    ["Schema", `v${health.schemaVersion}`],
    ["数据库", humanSize(health.databaseBytes)],
    ["WAL", humanSize(health.walBytes)],
    ["索引文件", health.indexedFiles.toLocaleString()],
    ["扫描会话", health.scanSessions.toLocaleString()],
    ["清理事务", health.cleanupTransactions.toLocaleString()],
    ["治理事件", health.governanceEvents.toLocaleString()]
  ].map(([label, value]) =>
    `<div class="metric"><strong>${escapeHTML(value)}</strong><span>${escapeHTML(label)}</span></div>`
  ).join("");
}

const auditStateLabel = (state) => ({
  completed: "完成", partial: "部分完成", failed: "失败",
  interrupted: "中断", executing: "执行中", prepared: "已准备",
  trashed: "已移入回收站", skipped_changed: "文件已变化", pending: "待处理"
}[state] || state);

function renderAudit(records) {
  $("auditSummary").textContent = records.length ? `最近 ${records.length} 条事务` : "尚无清理事务";
  $("auditRows").innerHTML = records.length ? records.map((record) => `
    <tr data-id="${escapeHTML(record.id)}">
      <td>${new Date(record.startedAt / 1e6).toLocaleString()}</td>
      <td>${escapeHTML(auditStateLabel(record.state))}</td>
      <td>${escapeHTML(record.policyId)}@${record.policyVersion}</td>
      <td>${record.itemCount.toLocaleString()} · 成功 ${record.trashedCount} · 失败 ${record.failedCount + record.changedCount}</td>
      <td>${humanSize(record.plannedBytes)}</td>
      <td>${humanSize(record.movedBytes)}</td>
    </tr>`).join("") :
    `<tr class="empty"><td colspan="6">尚无清理事务</td></tr>`;
}

async function loadGovernance() {
  const [policies, health, audit] = await Promise.all([
    api("/api/policies"),
    api("/api/maintenance"),
    api("/api/audit?limit=20")
  ]);
  renderPolicies(policies);
  renderHealth(health);
  renderAudit(audit);
}

async function maintainDatabase(action) {
  try {
    const health = await api("/api/maintenance", {
      method: "POST",
      body: JSON.stringify({action})
    });
    renderHealth(health);
    toast(action === "optimize" ? "数据库优化完成" : "WAL 空间回收完成");
  } catch (error) {
    toast(error.message);
  }
}

const weekdayNames = ["周日", "周一", "周二", "周三", "周四", "周五", "周六"];

function formatNanoTime(value) {
  if (!value) return "—";
  return new Date(Number(value) / 1e6).toLocaleString();
}

function renderSchedules(state) {
  schedulesState = state;
  $("scheduleBackend").textContent = `系统调度器：${state.backend}。计划使用当前用户权限运行。`;
  const alertLabels = {failure: "最近失败", budget: "预算耗尽", missed: "错过运行", drift: "任务漂移", backend: "注册失败"};
  const alerts = state.alerts || [];
  $("scheduleAlerts").classList.toggle("hidden", !alerts.length);
  $("scheduleAlerts").innerHTML = alerts.map((item) => `
    <div class="schedule-alert"><strong>${escapeHTML(alertLabels[item.kind] || "计划提醒")}</strong>
      <span>${escapeHTML(item.scheduleName)}：${escapeHTML(item.message)}</span></div>`).join("");
  $("scheduleSummary").textContent = state.schedules.length
    ? `${state.schedules.length} 个计划 · ${state.schedules.filter((item) => item.enabled).length} 个启用`
    : "尚未创建计划";
  $("scheduleCards").innerHTML = state.schedules.length ? state.schedules.map((item) => {
    const timing = item.cadence === "daily"
      ? `每天 ${String(item.hour).padStart(2, "0")}:${String(item.minute).padStart(2, "0")}`
      : `每周${weekdayNames[item.weekday]} ${String(item.hour).padStart(2, "0")}:${String(item.minute).padStart(2, "0")}`;
    const driftLabels = {ok: "正常", missing: "缺失", drifted: "已修改", unknown: "未知"};
    const driftLabel = item.driftState ? ` · 系统任务${driftLabels[item.driftState] || item.driftState}` : "";
    const runStatus = item.lastRunState === "failed"
      ? `最近失败：${item.lastRunMessage || "未提供原因"}`
      : item.lastRunState ? `最近状态：${item.lastRunState}` : "尚无运行记录";
    const nextRun = item.nextRunAt ? `预计下次：${formatNanoTime(item.nextRunAt)}` : "计划未启用";
    const missed = item.missedRuns ? ` · 错过 ${item.missedRuns} 次` : "";
    const budget = item.maxBytes || item.maxDurationSeconds
      ? ` · 预算 ${item.maxBytes ? humanSize(item.maxBytes) : "不限字节"} / ${item.maxDurationSeconds ? `${Math.floor(item.maxDurationSeconds / 60)} 分钟` : "不限时"}`
      : " · 不限资源预算";
    const needsRepair = item.backendState === "error" ||
      (item.driftState && item.driftState !== "ok");
    return `<article class="schedule-card" data-id="${escapeHTML(item.id)}">
      <div>
        <strong>${escapeHTML(item.name)} · ${item.enabled ? "已启用" : "已停用"}</strong>
        <span class="muted">${escapeHTML(timing)} · ${escapeHTML(item.backendState)}${escapeHTML(driftLabel)}</span>
        <span class="path">${escapeHTML(item.root)}</span>
        <span class="muted">${escapeHTML(nextRun)}${escapeHTML(missed)}${escapeHTML(budget)}</span>
        <span class="muted">${escapeHTML(runStatus)}</span>
      </div>
      <div class="schedule-card-actions">
        <button class="edit-schedule ghost">${iconSVG("edit")}编辑</button>
        <button class="run-schedule ghost">${iconSVG("scan")}立即扫描</button>
        <button class="toggle-schedule ghost">${iconSVG(item.enabled ? "stop" : "power")}${item.enabled ? "停用" : "启用"}</button>
        ${needsRepair ? `<button class="repair-schedule ghost">${iconSVG("refresh")}修复任务</button>` : ""}
        <button class="delete-schedule ghost">${iconSVG("trash")}删除</button>
      </div>
      ${item.backendError ? `<div class="backend-error">系统任务错误：${escapeHTML(item.backendError)}</div>` : ""}
      ${item.driftMessage && item.driftState !== "ok" ? `<div class="backend-error">任务状态：${escapeHTML(item.driftMessage)}</div>` : ""}
    </article>`;
  }).join("") : `<div class="muted">创建计划后，操作系统会在指定时间启动一次只读扫描。</div>`;
  $("scheduledRows").innerHTML = state.recent.length ? state.recent.map((item) => `
    <tr data-id="${escapeHTML(item.id)}">
      <td>${escapeHTML(formatNanoTime(item.startedAt))}</td>
      <td>${escapeHTML(item.scheduleName)}</td>
      <td>${escapeHTML(item.state)}</td>
      <td>${Number(item.filesSeen).toLocaleString()}</td>
      <td>${humanSize(item.bytesSeen)}</td>
      <td>${Number(item.resultCount).toLocaleString()}</td>
    </tr>`).join("") : `<tr class="empty"><td colspan="6">尚无计划扫描记录</td></tr>`;
}

async function loadSchedules() {
  renderSchedules(await api("/api/schedules"));
}

function resetScheduleForm() {
  $("scheduleId").value = "";
  $("scheduleName").value = "每周空间检查";
  $("scheduleRoot").value = $("root").value;
  $("scheduleCadence").value = "weekly";
  $("scheduleWeekday").value = "6";
  $("scheduleTime").value = "10:00";
  ["scheduleMinimum", "scheduleDuplicateMinimum"].forEach((id) => {
    [...$(id).options].filter((option) => option.dataset.custom === "true").forEach((option) => option.remove());
  });
  $("scheduleMinimum").value = $("minimum").value;
  $("scheduleDuplicateMinimum").value = $("duplicateMinimum").value;
  $("scheduleBudgetMiB").value = "0";
  $("scheduleBudgetMinutes").value = "0";
  $("scheduleExcludes").value = $("excludes").value;
  $("weekdayField").classList.remove("hidden");
  $("saveSchedule").textContent = "保存并启用";
}

function setScheduleSelectValue(id, value) {
  const select = $(id);
  const stringValue = String(value);
  if (![...select.options].some((option) => option.value === stringValue)) {
    const option = new Option(`${humanSize(value)}（自定义）`, stringValue);
    option.dataset.custom = "true";
    select.add(option);
  }
  select.value = stringValue;
}

function budgetValue(id, multiplier) {
  const value = Number($(id).value || 0);
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new Error("资源预算必须是非负整数");
  }
  const result = value * multiplier;
  if (!Number.isSafeInteger(result)) {
    throw new Error("资源预算过大");
  }
  return result;
}

async function saveScheduleForm() {
  const [hour, minute] = $("scheduleTime").value.split(":").map(Number);
  try {
    const state = await api("/api/schedules/save", {
      method: "POST",
      body: JSON.stringify({
        id: $("scheduleId").value,
        name: $("scheduleName").value,
        root: $("scheduleRoot").value,
        cadence: $("scheduleCadence").value,
        hour, minute,
        weekday: Number($("scheduleWeekday").value),
        minimum: Number($("scheduleMinimum").value),
        duplicateMinimum: Number($("scheduleDuplicateMinimum").value),
        maxBytes: budgetValue("scheduleBudgetMiB", 1024 * 1024),
        maxDurationSeconds: budgetValue("scheduleBudgetMinutes", 60),
        resultLimit: 2000,
        excludes: $("scheduleExcludes").value.split("\n").map((value) => value.trim()).filter(Boolean),
        enabled: true
      })
    });
    renderSchedules(state);
    resetScheduleForm();
    toast("扫描计划已保存；系统注册状态请查看计划卡片");
  } catch (error) {
    toast(error.message);
  }
}

async function startScan() {
  try {
    scanRoot = $("root").value;
    selected.clear();
    allRecords = [];
    duplicateGroups = [];
    currentFolder = "";
    $("folderSection").classList.add("hidden");
    $("duplicateSection").classList.add("hidden");
    renderFiles();
    renderPlan();
    await api("/api/scan", {
      method: "POST",
      body: JSON.stringify({
        path: scanRoot,
        minimum: Number($("minimum").value),
        duplicateMinimum: Number($("duplicateMinimum").value),
        excludes: $("excludes").value.split("\n").map((value) => value.trim()).filter(Boolean),
        limit: 5000,
        maxBytes: budgetValue("budgetMiB", 1024 * 1024),
        maxDurationSeconds: budgetValue("budgetMinutes", 60)
      })
    });
    renderDashboard({state: "running", phase: "walking", root: scanRoot, filesSeen: 0, bytesSeen: 0, errors: 0, excluded: 0});
    setRunning(true);
    $("status").textContent = "正在扫描…";
    $("detail").textContent = "扫描过程中会持续更新大文件，目录排行将在扫描结束后生成。";
    clearInterval(pollTimer);
    pollTimer = setInterval(updateStatus, 500);
    updateStatus();
  } catch (error) { toast(error.message); }
}

async function updateStatus() {
  try {
    const status = await api("/api/status");
    renderDashboard(status);
    if (status.results) {
      allRecords = status.results;
      updateCategories();
      renderFiles();
    }
    if (status.state === "running") {
      $("detail").textContent = status.phase === "duplicates"
        ? `正在核对重复内容 · 指纹 ${(status.filesFingerprinted || 0).toLocaleString()} 个 · 新计算 ${status.filesHashed.toLocaleString()} 个哈希 · 复用 ${(status.hashesReused || 0).toLocaleString()} 个 · ${status.currentPath || ""}`
        : `已检查 ${status.filesSeen.toLocaleString()} 个文件（${humanSize(status.bytesSeen)}） · ${status.currentPath || ""}`;
      return;
    }
    clearInterval(pollTimer);
    setRunning(false);
    if (["done", "cancelled", "budget_exceeded"].includes(status.state)) {
      $("status").textContent = status.state === "done" ? "扫描完成"
        : status.state === "budget_exceeded" ? "扫描达到预算" : "扫描已停止";
      $("detail").textContent = `策略 ${status.policyId}@${status.policyVersion} · 检查 ${status.filesSeen.toLocaleString()} 个文件 · 规则排除 ${status.excluded.toLocaleString()} 项 · 读取错误 ${status.errors.toLocaleString()} 项 · 指纹 ${(status.filesFingerprinted || 0).toLocaleString()} 个 · 新计算 ${status.filesHashed.toLocaleString()} 个哈希 · 复用 ${(status.hashesReused || 0).toLocaleString()} 个 · ${(status.elapsedMs / 1000).toFixed(1)} 秒${status.budgetExceeded ? ` · ${status.budgetExceeded}` : ""}`;
      allRecords = status.results || [];
      duplicateGroups = status.duplicateGroups || [];
      updateCategories();
      renderFiles();
      renderDuplicates();
      renderPlan();
      scanRoot = status.root;
      await loadFolder(status.root);
      await loadSchedules();
    } else if (status.state === "error") {
      $("status").textContent = "扫描失败";
      toast(status.message);
      await loadSchedules();
    }
  } catch (error) {
    clearInterval(pollTimer);
    setRunning(false);
    toast(error.message);
  }
}

async function loadFolder(path) {
  try {
    const view = await api(`/api/folders?path=${encodeURIComponent(path)}`);
    currentFolder = view.current.path;
    $("folderSection").classList.remove("hidden");
    $("folderPath").textContent = view.current.path;
    $("folderUp").disabled = !view.parent;
    $("folderUp").dataset.path = view.parent || "";
    renderFolders(view);
  } catch (error) {
    toast(error.message);
  }
}

function renderFolders(view) {
  const total = Math.max(1, view.current.size);
  if (!view.children.length) {
    $("folderRows").innerHTML = `<tr class="empty"><td colspan="5">此目录没有已扫描的子文件夹</td></tr>`;
    return;
  }
  $("folderRows").innerHTML = view.children.map((folder) => {
    const percent = Math.min(100, folder.size / total * 100);
    return `<tr class="folder-row" data-path="${escapeHTML(folder.path)}">
      <td><div class="folder-name">${escapeHTML(folder.name)}</div><div class="muted path">${escapeHTML(folder.path)}</div></td>
      <td>${humanSize(folder.size)}</td>
      <td>${folder.fileCount.toLocaleString()}</td>
      <td>${escapeHTML(folder.modifiedAt || "—")}</td>
      <td><span class="percent"><i style="--percent:${percent.toFixed(1)}%"></i>${percent.toFixed(1)}%</span></td>
    </tr>`;
  }).join("");
}

function recordByPath(path) {
  const large = allRecords.find((item) => item.path === path);
  if (large) return large;
  for (const group of duplicateGroups) {
    const duplicate = group.files.find((item) => item.path === path);
    if (duplicate) return duplicate;
  }
  return persistedPlanRecords.get(path) || null;
}

async function loadPlan() {
  const records = await api("/api/plan");
  persistedPlanRecords = new Map(records.map((record) => [record.path, record]));
  plan = new Set(records.map((record) => record.path));
  renderPlan();
}

function persistPlan() {
  const paths = [...plan];
  planSave = planSave.then(async () => {
    try {
      const records = await api("/api/plan", {
        method: "POST",
        body: JSON.stringify({paths})
      });
      persistedPlanRecords = new Map(records.map((record) => [record.path, record]));
    } catch (error) {
      toast(error.message);
      await loadPlan();
      renderFiles();
      renderDuplicates();
    }
  });
  return planSave;
}

function duplicateGroupFor(path) {
  return duplicateGroups.find((group) => group.files.some((item) => item.path === path));
}

function addToPlan(path) {
  const record = recordByPath(path);
  if (!record || record.advice.level === "danger" || plan.has(path)) return false;
  const group = duplicateGroupFor(path);
  if (group) {
    const plannedCopies = group.files.filter((item) => plan.has(item.path) || item.path === path).length;
    if (plannedCopies >= group.files.length) {
      toast("每组重复文件必须至少保留一个副本");
      return false;
    }
  }
  plan.add(path);
  return true;
}

function renderDuplicates() {
  const reclaimable = duplicateGroups.reduce((sum, group) => sum + group.reclaimable, 0);
  $("duplicateSummary").textContent = duplicateGroups.length
    ? `${duplicateGroups.length} 组 · ${duplicateGroups.reduce((sum, group) => sum + group.files.length, 0)} 个文件 · 最多可释放 ${humanSize(reclaimable)}`
    : "未发现内容完全相同的文件";
  $("duplicateSection").classList.remove("hidden");
  if (!duplicateGroups.length) {
    $("duplicateGroups").innerHTML = `<div class="muted">没有发现达到重复检测体积阈值的重复文件。</div>`;
    renderDashboard();
    return;
  }
  $("duplicateGroups").innerHTML = duplicateGroups.map((group) => `
    <article class="duplicate-card" data-group="${escapeHTML(group.id)}">
      <div class="duplicate-card-head">
        <div><strong>${group.files.length} 个相同文件 · 每个 ${humanSize(group.size)}</strong><span class="muted">可释放 ${humanSize(group.reclaimable)} · 内容指纹 ${escapeHTML(group.id)}</span></div>
        <button class="add-duplicate-copies ghost">${iconSVG("copy")}将其余副本加入计划</button>
      </div>
      <div class="duplicate-files">
        ${group.files.map((item, index) => `
          <div class="duplicate-file ${index === 0 ? "keep" : ""}" data-path="${escapeHTML(item.path)}">
            <span class="${index === 0 ? "keep-label" : "muted"}">${index === 0 ? "建议保留" : "重复副本"}</span>
            <span>${escapeHTML(item.modifiedAt)}</span>
            <span class="path">${escapeHTML(item.path)}</span>
            <button class="add-plan ghost" ${item.advice.level === "danger" || plan.has(item.path) ? "disabled" : ""}>${iconSVG(plan.has(item.path) ? "check" : "plus")}${plan.has(item.path) ? "已在计划" : "加入计划"}</button>
          </div>`).join("")}
      </div>
    </article>`).join("");
  renderDashboard();
}

function planRecords() {
  return [...plan].map(recordByPath).filter(Boolean);
}

function renderPlan() {
  const records = planRecords();
  const total = records.reduce((sum, item) => sum + item.size, 0);
  $("planSummary").textContent = records.length
    ? `${records.length} 个文件 · 待移入回收站 ${humanSize(total)}`
    : "尚未加入文件";
  $("clearPlan").disabled = records.length === 0;
  $("executePlan").disabled = records.length === 0 || $("scan").disabled;
  if (!records.length) {
    $("planRows").innerHTML = `<tr class="empty"><td colspan="4">从大文件或重复文件中加入待清理项目</td></tr>`;
    renderDashboard();
    return;
  }
  $("planRows").innerHTML = records.sort((a, b) => b.size - a.size).map((item) => `
    <tr data-path="${escapeHTML(item.path)}">
      <td>${humanSize(item.size)}</td>
      <td><span class="badge ${item.advice.level}"><span>${escapeHTML(item.advice.label)}</span></span></td>
      <td><div class="path">${escapeHTML(item.path)}</div><div class="reason">${escapeHTML(item.advice.reason)}</div></td>
      <td><button class="remove-plan ghost">${iconSVG("clear")}移出计划</button></td>
    </tr>`).join("");
  renderDashboard();
}

function updateCategories() {
  const previous = $("categoryFilter").value;
  const categories = [...new Set(allRecords.map((item) => item.advice.category))].sort();
  $("categoryFilter").innerHTML = `<option value="">全部类型</option>` +
    categories.map((category) => `<option value="${escapeHTML(category)}">${escapeHTML(category)}</option>`).join("");
  if (categories.includes(previous)) $("categoryFilter").value = previous;
}

function visibleRecords() {
  const query = $("search").value.trim().toLocaleLowerCase();
  const advice = $("adviceFilter").value;
  const category = $("categoryFilter").value;
  const records = allRecords.filter((item) =>
    (!query || item.path.toLocaleLowerCase().includes(query)) &&
    (!advice || item.advice.level === advice) &&
    (!category || item.advice.category === category)
  );
  const order = $("sort").value;
  records.sort((a, b) => {
    if (order === "size-asc") return a.size - b.size || a.path.localeCompare(b.path);
    if (order === "oldest") return a.modifiedAt.localeCompare(b.modifiedAt) || b.size - a.size;
    if (order === "newest") return b.modifiedAt.localeCompare(a.modifiedAt) || b.size - a.size;
    if (order === "path") return a.path.localeCompare(b.path);
    return b.size - a.size || a.path.localeCompare(b.path);
  });
  return records;
}

function renderFiles() {
  const records = visibleRecords();
  $("count").textContent = allRecords.length
    ? `显示 ${records.length.toLocaleString()} / ${allRecords.length.toLocaleString()} 个文件`
    : "尚未扫描";
  if (!records.length) {
    $("rows").innerHTML = `<tr class="empty"><td colspan="6">${allRecords.length ? "没有符合筛选条件的文件" : "扫描结果会显示在这里"}</td></tr>`;
    updateSelection();
    return;
  }
  $("rows").innerHTML = records.map((item) => {
    const disabled = item.advice.level === "danger";
    return `<tr data-path="${escapeHTML(item.path)}">
      <td class="check-col"><input class="row-check" type="checkbox" ${selected.has(item.path) ? "checked" : ""} ${disabled ? "disabled" : ""} aria-label="选择 ${escapeHTML(item.path)}"></td>
      <td>${humanSize(item.size)}</td>
      <td>${escapeHTML(item.modifiedAt)}</td>
      <td><span class="badge ${item.advice.level}"><span>${escapeHTML(item.advice.label)}</span></span></td>
      <td>
        <div class="path">${escapeHTML(item.path)}</div>
        <div class="reason">${escapeHTML(item.advice.reason)}<span class="rule">${escapeHTML(item.advice.ruleId)} · ${item.advice.score}</span></div>
      </td>
      <td><button class="add-plan ghost" ${disabled || plan.has(item.path) ? "disabled" : ""}>${iconSVG(plan.has(item.path) ? "check" : "plus")}${plan.has(item.path) ? "已在计划" : "加入计划"}</button></td>
    </tr>`;
  }).join("");
  updateSelection();
}

function updateSelection() {
  const selectedRecords = allRecords.filter((item) => selected.has(item.path));
  const total = selectedRecords.reduce((sum, item) => sum + item.size, 0);
  $("selectionSummary").textContent = selectedRecords.length
    ? `已选择 ${selectedRecords.length} 个文件，待移入回收站 ${humanSize(total)}`
    : "尚未选择文件";
  $("clearSelection").disabled = selectedRecords.length === 0;
  $("trashSelected").disabled = selectedRecords.length === 0 || $("scan").disabled;
  const selectable = visibleRecords().filter((item) => item.advice.level !== "danger");
  $("selectVisible").checked = selectable.length > 0 && selectable.every((item) => selected.has(item.path));
  $("selectVisible").indeterminate = selectable.some((item) => selected.has(item.path)) && !$("selectVisible").checked;
}

async function executeCleanupPlan() {
  const paths = [...plan];
  const records = planRecords();
  const total = records.reduce((sum, item) => sum + item.size, 0);
  if (!confirm(`确定将 ${records.length} 个文件移入系统回收站吗？\n\n待移入回收站：${humanSize(total)}\n\n文件可从回收站恢复；磁盘空间需清空回收站后才可能增加。`)) return;
  try {
    const response = await api("/api/trash-batch", {
      method: "POST",
      body: JSON.stringify({paths})
    });
    const succeeded = new Set(response.results.filter((item) => !item.error).map((item) => item.path));
    const failed = response.results.filter((item) => item.error);
    const auditFailures = response.results.filter((item) => item.auditError);
    succeeded.forEach((path) => selected.delete(path));
    succeeded.forEach((path) => plan.delete(path));
    succeeded.forEach((path) => persistedPlanRecords.delete(path));
    const latest = await api("/api/status");
    renderDashboard(latest);
    allRecords = latest.results || [];
    duplicateGroups = latest.duplicateGroups || [];
    updateCategories();
    renderFiles();
    renderDuplicates();
    renderPlan();
    if (currentFolder) await loadFolder(currentFolder);
    await refreshDisk();
    await loadGovernance();
    toast(`已移入回收站 ${succeeded.size} 个文件，逻辑大小 ${humanSize(response.movedBytes)}`);
    if (response.transactionId) {
      console.info(`Space Sheriff cleanup transaction: ${response.transactionId}`);
    }
    if (failed.length) {
      alert(`以下 ${failed.length} 个文件未清理：\n\n` + failed.slice(0, 10).map((item) => `${item.path}\n${item.error}`).join("\n\n"));
    }
    if (auditFailures.length) {
      alert("文件操作已完成，但本地审计记录不完整：\n\n" +
        auditFailures.map((item) => item.auditError).join("\n"));
    }
  } catch (error) {
    toast(error.message);
  }
}

function addSelectedToPlan() {
  let added = 0;
  for (const path of selected) {
    if (addToPlan(path)) added++;
  }
  selected.clear();
  renderFiles();
  renderDuplicates();
  renderPlan();
  persistPlan();
  toast(`已将 ${added} 个文件加入清理计划`);
}

$("rows").addEventListener("change", (event) => {
  const checkbox = event.target.closest(".row-check");
  if (!checkbox) return;
  const path = checkbox.closest("tr").dataset.path;
  if (checkbox.checked) selected.add(path); else selected.delete(path);
  updateSelection();
});

$("rows").addEventListener("click", (event) => {
  const button = event.target.closest(".add-plan");
  if (!button) return;
  if (addToPlan(button.closest("tr").dataset.path)) {
    renderFiles();
    renderDuplicates();
    renderPlan();
    persistPlan();
  }
});

$("folderRows").addEventListener("click", (event) => {
  const row = event.target.closest(".folder-row");
  if (row) loadFolder(row.dataset.path);
});

$("duplicateGroups").addEventListener("click", (event) => {
  const addButton = event.target.closest(".add-plan");
  if (addButton) {
    const path = addButton.closest(".duplicate-file").dataset.path;
    if (addToPlan(path)) {
      renderFiles();
      renderDuplicates();
      renderPlan();
      persistPlan();
    }
    return;
  }
  const copiesButton = event.target.closest(".add-duplicate-copies");
  if (!copiesButton) return;
  const groupID = copiesButton.closest(".duplicate-card").dataset.group;
  const group = duplicateGroups.find((item) => item.id === groupID);
  if (!group) return;
  let added = 0;
  group.files.slice(1).forEach((item) => {
    if (addToPlan(item.path)) added++;
  });
  renderFiles();
  renderDuplicates();
  renderPlan();
  persistPlan();
  toast(`已加入 ${added} 个重复副本，并保留最新文件`);
});

$("planRows").addEventListener("click", (event) => {
  const button = event.target.closest(".remove-plan");
  if (!button) return;
  plan.delete(button.closest("tr").dataset.path);
  renderFiles();
  renderDuplicates();
  renderPlan();
  persistPlan();
});

$("folderUp").addEventListener("click", () => {
  if ($("folderUp").dataset.path) loadFolder($("folderUp").dataset.path);
});

["search", "adviceFilter", "categoryFilter", "sort"].forEach((id) => {
  $(id).addEventListener(id === "search" ? "input" : "change", renderFiles);
});

$("selectVisible").addEventListener("change", () => {
  visibleRecords().filter((item) => item.advice.level !== "danger").forEach((item) => {
    if ($("selectVisible").checked) selected.add(item.path); else selected.delete(item.path);
  });
  renderFiles();
});

$("clearSelection").addEventListener("click", () => {
  selected.clear();
  renderFiles();
});

$("trashSelected").addEventListener("click", addSelectedToPlan);
$("clearPlan").addEventListener("click", () => {
  plan.clear();
  renderFiles();
  renderDuplicates();
  renderPlan();
  persistPlan();
});
$("executePlan").addEventListener("click", executeCleanupPlan);

$("scheduleCadence").addEventListener("change", () => {
  $("weekdayField").classList.toggle("hidden", $("scheduleCadence").value === "daily");
});
$("saveSchedule").addEventListener("click", saveScheduleForm);
$("resetSchedule").addEventListener("click", resetScheduleForm);
$("refreshSchedules").addEventListener("click", () => loadSchedules().catch((error) => toast(error.message)));
$("scheduleCards").addEventListener("click", async (event) => {
  const card = event.target.closest(".schedule-card");
  if (!card) return;
  const schedule = schedulesState.schedules.find((item) => item.id === card.dataset.id);
  if (!schedule) return;
  if (event.target.closest(".edit-schedule")) {
    $("scheduleId").value = schedule.id;
    $("scheduleName").value = schedule.name;
    $("scheduleRoot").value = schedule.root;
    $("scheduleCadence").value = schedule.cadence;
    $("scheduleWeekday").value = String(schedule.weekday);
    $("scheduleTime").value = `${String(schedule.hour).padStart(2, "0")}:${String(schedule.minute).padStart(2, "0")}`;
    setScheduleSelectValue("scheduleMinimum", schedule.minimum);
    setScheduleSelectValue("scheduleDuplicateMinimum", schedule.duplicateMinimum);
    $("scheduleBudgetMiB").value = schedule.maxBytes ? Math.floor(schedule.maxBytes / (1024 * 1024)) : "0";
    $("scheduleBudgetMinutes").value = schedule.maxDurationSeconds ? Math.floor(schedule.maxDurationSeconds / 60) : "0";
    $("scheduleExcludes").value = schedule.excludes.join("\n");
    $("weekdayField").classList.toggle("hidden", schedule.cadence === "daily");
    $("saveSchedule").textContent = "保存修改并启用";
    return;
  }
  try {
    let state;
    if (event.target.closest(".run-schedule")) {
      await api("/api/schedules/run", {method: "POST", body: JSON.stringify({id: schedule.id})});
      toast("计划扫描已开始，可在状态栏查看进度");
      clearInterval(pollTimer);
      pollTimer = setInterval(updateStatus, 500);
      updateStatus();
      return;
    }
    if (event.target.closest(".toggle-schedule")) {
      state = await api("/api/schedules/toggle", {
        method: "POST", body: JSON.stringify({id: schedule.id, enabled: !schedule.enabled})
      });
    } else if (event.target.closest(".repair-schedule")) {
      state = await api("/api/schedules/repair", {
        method: "POST", body: JSON.stringify({id: schedule.id})
      });
      toast("已重新同步系统任务，请查看计划状态");
    } else if (event.target.closest(".delete-schedule")) {
      if (!confirm(`删除扫描计划“${schedule.name}”？历史扫描记录会保留。`)) return;
      state = await api("/api/schedules/delete", {
        method: "POST", body: JSON.stringify({id: schedule.id})
      });
    } else {
      return;
    }
    renderSchedules(state);
  } catch (error) {
    toast(error.message);
    loadSchedules().catch(() => {});
  }
});

$("scheduledRows").addEventListener("click", async (event) => {
  const row = event.target.closest("tr[data-id]");
  if (!row) return;
  try {
    const detail = await api(`/api/scheduled-scans?id=${encodeURIComponent(row.dataset.id)}`);
    $("scheduledDetail").innerHTML = `
      <strong>${escapeHTML(detail.summary.scheduleName)} · ${escapeHTML(detail.summary.state)}</strong>
      <span class="muted"> · 策略 ${escapeHTML(detail.summary.policyId)}@${detail.summary.policyVersion}</span>
      <div class="audit-items">${detail.findings.map((item) => `
        <div class="audit-item">
          <strong>${humanSize(item.size)}</strong>
          <span>${escapeHTML(item.advice.label)}</span>
          <div class="path">${escapeHTML(item.path)}<div class="reason">${escapeHTML(item.modifiedAt)} · ${escapeHTML(item.advice.reason)}</div></div>
        </div>`).join("") || '<span class="muted">本次运行没有保存结果。</span>'}</div>`;
  } catch (error) {
    toast(error.message);
  }
});

$("policySelect").addEventListener("change", () => {
  renderPolicy(policyByID($("policySelect").value));
});

$("activatePolicy").addEventListener("click", async () => {
  try {
    const state = await api("/api/policies/activate", {
      method: "POST",
      body: JSON.stringify({id: $("policySelect").value})
    });
    renderPolicies(state);
    toast(`已启用策略：${policyByID(state.activeId).name}`);
  } catch (error) {
    toast(error.message);
  }
});

$("importPolicy").addEventListener("click", async () => {
  try {
    const policy = await api("/api/policies/import", {
      method: "POST",
      body: $("policyJson").value
    });
    await loadGovernance();
    $("policySelect").value = policy.id;
    renderPolicy(policyByID(policy.id));
    toast(`已导入策略：${policy.name} v${policy.version}`);
  } catch (error) {
    toast(error.message);
  }
});

$("checkpointDatabase").addEventListener("click", () => maintainDatabase("checkpoint"));
$("optimizeDatabase").addEventListener("click", () => maintainDatabase("optimize"));
$("refreshGovernance").addEventListener("click", () => loadGovernance().catch((error) => toast(error.message)));

$("auditRows").addEventListener("click", async (event) => {
  const row = event.target.closest("tr[data-id]");
  if (!row) return;
  try {
    const detail = await api(`/api/audit?id=${encodeURIComponent(row.dataset.id)}`);
    $("auditDetail").innerHTML = `
      <strong>${escapeHTML(auditStateLabel(detail.state))} · ${escapeHTML(detail.policyId)}@${detail.policyVersion}</strong>
      <span class="muted"> · 计划 ${humanSize(detail.plannedBytes)} · 已移入回收站 ${humanSize(detail.movedBytes)}</span>
      <div class="audit-items">${detail.items.map((item) => `
        <div class="audit-item">
          <strong>${escapeHTML(auditStateLabel(item.state))}</strong>
          <span>${humanSize(item.size)}</span>
          <div class="path">${escapeHTML(item.path)}${item.error ? `<div class="reason">${escapeHTML(item.error)}</div>` : ""}</div>
        </div>`).join("")}</div>`;
  } catch (error) {
    toast(error.message);
  }
});

function exportRows() {
  const files = new Map(allRecords.map((item) => [item.path, item]));
  const groupByPath = new Map();
  duplicateGroups.forEach((group) => group.files.forEach((item) => {
    files.set(item.path, item);
    groupByPath.set(item.path, group.id);
  }));
  return [...files.values()].map((item) => ({
    path: item.path,
    size: item.size,
    sizeHuman: humanSize(item.size),
    modifiedAt: item.modifiedAt,
    advice: item.advice.label,
    category: item.advice.category,
    ruleId: item.advice.ruleId,
    reason: item.advice.reason,
    nextStep: nextStepForRecord(item),
    duplicateGroup: groupByPath.get(item.path) || ""
  }));
}

function downloadExport(content, type, extension) {
  const blob = new Blob([content], {type});
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  const filename = `space-sheriff-${new Date().toISOString().slice(0, 10)}.${extension}`;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  return filename;
}

function exportReady() {
  if (!["done", "budget_exceeded", "cancelled"].includes(latestStatus.state)) {
    toast("请先完成或停止一次扫描，再导出报告");
    return false;
  }
  return true;
}

$("exportJson").addEventListener("click", () => {
  if (!exportReady()) return;
  const payload = {
    version: $("version").textContent,
    exportedAt: new Date().toISOString(),
    root: scanRoot,
    summary: reportSummary(),
    files: exportRows(),
    duplicateGroups
  };
  const filename = downloadExport(JSON.stringify(payload, null, 2), "application/json", "json");
  toast(`已导出 ${filename}，请在下载文件夹查看`);
});

$("exportCsv").addEventListener("click", () => {
  if (!exportReady()) return;
  const fields = ["path", "size", "sizeHuman", "modifiedAt", "advice", "category", "ruleId", "reason", "nextStep", "duplicateGroup"];
  const quote = (value) => `"${String(value).replaceAll('"', '""')}"`;
  const csv = [fields.join(","), ...exportRows().map((row) => fields.map((field) => quote(row[field])).join(","))].join("\r\n");
  const filename = downloadExport("\ufeff" + csv, "text/csv;charset=utf-8", "csv");
  toast(`已导出 ${filename}，请在下载文件夹查看`);
});

$("roots").addEventListener("change", () => {
  const root = roots[Number($("roots").value)];
  if (root) { $("root").value = root.path; showDisk(root); }
});

$("root").addEventListener("input", () => showDisk(roots.find((item) => item.path === $("root").value)));
$("scan").addEventListener("click", startScan);
$("cancel").addEventListener("click", () => api("/api/cancel", {method:"POST", body:"{}"}));
$("quit").addEventListener("click", async () => {
  await api("/api/quit", {method:"POST", body:"{}"});
  document.body.innerHTML = `<main><section class="card status-card"><strong>空间卫士已退出，可以关闭此页面。</strong></section></main>`;
});

Promise.all([loadRoots(), api("/api/version"), loadPlan(), loadGovernance(), loadSchedules()])
  .then(([, info]) => { $("version").textContent = `v${info.version}`; })
  .catch((error) => toast(error.message));
