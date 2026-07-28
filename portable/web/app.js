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
let currentFolder = "";
let scanRoot = "";

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

async function api(path, options = {}) {
  const headers = {...(options.headers || {}), "X-Space-Sheriff-Token": SESSION_TOKEN};
  if (options.body) headers["Content-Type"] = "application/json";
  const response = await fetch(path, {...options, headers});
  if (!response.ok) throw new Error((await response.text()).trim() || `请求失败 (${response.status})`);
  return response.json();
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
    showDisk(roots[0]);
  }
}

async function refreshDisk() {
  roots = await api("/api/roots");
  showDisk(roots.find((item) => item.path === scanRoot) || roots[0]);
}

function setRunning(running) {
  $("scan").disabled = running;
  $("cancel").disabled = !running;
  $("pulse").classList.toggle("active", running);
  $("trashSelected").disabled = running || selected.size === 0;
  $("executePlan").disabled = running || plan.size === 0;
}

async function startScan() {
  try {
    scanRoot = $("root").value;
    selected.clear();
    plan.clear();
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
        limit: 5000
      })
    });
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
    if (status.results) {
      allRecords = status.results;
      updateCategories();
      renderFiles();
    }
    if (status.state === "running") {
      $("detail").textContent = status.phase === "duplicates"
        ? `正在核对重复内容 · 已计算 ${status.filesHashed.toLocaleString()} 个文件哈希 · ${status.currentPath || ""}`
        : `已检查 ${status.filesSeen.toLocaleString()} 个文件（${humanSize(status.bytesSeen)}） · ${status.currentPath || ""}`;
      return;
    }
    clearInterval(pollTimer);
    setRunning(false);
    if (["done", "cancelled"].includes(status.state)) {
      $("status").textContent = status.state === "done" ? "扫描完成" : "扫描已停止";
      $("detail").textContent = `检查 ${status.filesSeen.toLocaleString()} 个文件 · 排除 ${status.excluded.toLocaleString()} 项 · 核对 ${status.filesHashed.toLocaleString()} 个候选 · ${status.errors.toLocaleString()} 个错误 · ${(status.elapsedMs / 1000).toFixed(1)} 秒`;
      allRecords = status.results || [];
      duplicateGroups = status.duplicateGroups || [];
      updateCategories();
      renderFiles();
      renderDuplicates();
      renderPlan();
      scanRoot = status.root;
      await loadFolder(status.root);
    } else if (status.state === "error") {
      $("status").textContent = "扫描失败";
      toast(status.message);
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
  return null;
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
    return;
  }
  $("duplicateGroups").innerHTML = duplicateGroups.map((group) => `
    <article class="duplicate-card" data-group="${escapeHTML(group.id)}">
      <div class="duplicate-card-head">
        <div><strong>${group.files.length} 个相同文件 · 每个 ${humanSize(group.size)}</strong><span class="muted">可释放 ${humanSize(group.reclaimable)} · 内容指纹 ${escapeHTML(group.id)}</span></div>
        <button class="add-duplicate-copies ghost">将其余副本加入计划</button>
      </div>
      <div class="duplicate-files">
        ${group.files.map((item, index) => `
          <div class="duplicate-file ${index === 0 ? "keep" : ""}" data-path="${escapeHTML(item.path)}">
            <span class="${index === 0 ? "keep-label" : "muted"}">${index === 0 ? "建议保留" : "重复副本"}</span>
            <span>${escapeHTML(item.modifiedAt)}</span>
            <span class="path">${escapeHTML(item.path)}</span>
            <button class="add-plan ghost" ${item.advice.level === "danger" || plan.has(item.path) ? "disabled" : ""}>${plan.has(item.path) ? "已在计划" : "加入计划"}</button>
          </div>`).join("")}
      </div>
    </article>`).join("");
}

function planRecords() {
  return [...plan].map(recordByPath).filter(Boolean);
}

function renderPlan() {
  const records = planRecords();
  const total = records.reduce((sum, item) => sum + item.size, 0);
  $("planSummary").textContent = records.length
    ? `${records.length} 个文件 · 预计释放 ${humanSize(total)}`
    : "尚未加入文件";
  $("clearPlan").disabled = records.length === 0;
  $("executePlan").disabled = records.length === 0 || $("scan").disabled;
  if (!records.length) {
    $("planRows").innerHTML = `<tr class="empty"><td colspan="4">从大文件或重复文件中加入待清理项目</td></tr>`;
    return;
  }
  $("planRows").innerHTML = records.sort((a, b) => b.size - a.size).map((item) => `
    <tr data-path="${escapeHTML(item.path)}">
      <td>${humanSize(item.size)}</td>
      <td><span class="badge ${item.advice.level}"><span>${escapeHTML(item.advice.label)}</span></span></td>
      <td><div class="path">${escapeHTML(item.path)}</div><div class="reason">${escapeHTML(item.advice.reason)}</div></td>
      <td><button class="remove-plan ghost">移出计划</button></td>
    </tr>`).join("");
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
      <td><button class="add-plan ghost" ${disabled || plan.has(item.path) ? "disabled" : ""}>${plan.has(item.path) ? "已在计划" : "加入计划"}</button></td>
    </tr>`;
  }).join("");
  updateSelection();
}

function updateSelection() {
  const selectedRecords = allRecords.filter((item) => selected.has(item.path));
  const total = selectedRecords.reduce((sum, item) => sum + item.size, 0);
  $("selectionSummary").textContent = selectedRecords.length
    ? `已选择 ${selectedRecords.length} 个文件，预计释放 ${humanSize(total)}`
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
  if (!confirm(`确定将 ${records.length} 个文件移入系统回收站吗？\n\n预计释放：${humanSize(total)}\n\n文件可从回收站恢复。`)) return;
  try {
    const response = await api("/api/trash-batch", {
      method: "POST",
      body: JSON.stringify({paths})
    });
    const succeeded = new Set(response.results.filter((item) => !item.error).map((item) => item.path));
    const failed = response.results.filter((item) => item.error);
    succeeded.forEach((path) => selected.delete(path));
    succeeded.forEach((path) => plan.delete(path));
    const latest = await api("/api/status");
    allRecords = latest.results || [];
    duplicateGroups = latest.duplicateGroups || [];
    updateCategories();
    renderFiles();
    renderDuplicates();
    renderPlan();
    if (currentFolder) await loadFolder(currentFolder);
    await refreshDisk();
    toast(`已移入回收站 ${succeeded.size} 个文件，实际释放 ${humanSize(response.released)}`);
    if (failed.length) {
      alert(`以下 ${failed.length} 个文件未清理：\n\n` + failed.slice(0, 10).map((item) => `${item.path}\n${item.error}`).join("\n\n"));
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
  toast(`已加入 ${added} 个重复副本，并保留最新文件`);
});

$("planRows").addEventListener("click", (event) => {
  const button = event.target.closest(".remove-plan");
  if (!button) return;
  plan.delete(button.closest("tr").dataset.path);
  renderFiles();
  renderDuplicates();
  renderPlan();
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
});
$("executePlan").addEventListener("click", executeCleanupPlan);

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
    modifiedAt: item.modifiedAt,
    advice: item.advice.label,
    category: item.advice.category,
    ruleId: item.advice.ruleId,
    reason: item.advice.reason,
    duplicateGroup: groupByPath.get(item.path) || ""
  }));
}

function downloadExport(content, type, extension) {
  const blob = new Blob([content], {type});
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = `space-sheriff-${new Date().toISOString().slice(0, 10)}.${extension}`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(link.href), 1000);
}

$("exportJson").addEventListener("click", () => {
  const payload = {
    version: $("version").textContent,
    exportedAt: new Date().toISOString(),
    root: scanRoot,
    files: exportRows(),
    duplicateGroups
  };
  downloadExport(JSON.stringify(payload, null, 2), "application/json", "json");
});

$("exportCsv").addEventListener("click", () => {
  const fields = ["path", "size", "modifiedAt", "advice", "category", "ruleId", "reason", "duplicateGroup"];
  const quote = (value) => `"${String(value).replaceAll('"', '""')}"`;
  const csv = [fields.join(","), ...exportRows().map((row) => fields.map((field) => quote(row[field])).join(","))].join("\r\n");
  downloadExport("\ufeff" + csv, "text/csv;charset=utf-8", "csv");
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

Promise.all([loadRoots(), api("/api/version")])
  .then(([, info]) => { $("version").textContent = `v${info.version}`; })
  .catch((error) => toast(error.message));
