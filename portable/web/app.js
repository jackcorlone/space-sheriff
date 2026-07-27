const $ = (id) => document.getElementById(id);
let roots = [];
let pollTimer = null;

const humanSize = (bytes) => {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = Number(bytes), unit = 0;
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
  return `${unit ? value.toFixed(1) : Math.round(value)} ${units[unit]}`;
};

async function api(path, options = {}) {
  const response = await fetch(path, options);
  if (!response.ok) throw new Error((await response.text()).trim() || `请求失败 (${response.status})`);
  return response.json();
}

function toast(message) {
  $("toast").textContent = message;
  $("toast").classList.add("show");
  setTimeout(() => $("toast").classList.remove("show"), 3000);
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
  $("roots").innerHTML = roots.map((root, index) => `<option value="${index}">${escapeHTML(root.label)}</option>`).join("");
  if (roots.length) {
    $("root").value = roots[0].path;
    showDisk(roots[0]);
  }
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (char) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
}

function setRunning(running) {
  $("scan").disabled = running;
  $("cancel").disabled = !running;
  $("pulse").classList.toggle("active", running);
}

async function startScan() {
  try {
    await api("/api/scan", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({path: $("root").value, minimum: Number($("minimum").value), limit: 2000})
    });
    setRunning(true);
    $("status").textContent = "正在扫描…";
    $("detail").textContent = "首次扫描整个磁盘可能需要几分钟，可以随时停止。";
    $("rows").innerHTML = `<tr class="empty"><td colspan="5">正在查找大文件…</td></tr>`;
    clearInterval(pollTimer);
    pollTimer = setInterval(updateStatus, 500);
    updateStatus();
  } catch (error) { toast(error.message); }
}

async function updateStatus() {
  try {
    const status = await api("/api/status");
    if (status.state === "running") {
      $("detail").textContent = `已检查 ${status.filesSeen.toLocaleString()} 个文件（${humanSize(status.bytesSeen)}） · ${status.currentPath || ""}`;
      return;
    }
    clearInterval(pollTimer);
    setRunning(false);
    if (["done", "cancelled"].includes(status.state)) {
      $("status").textContent = status.state === "done" ? "扫描完成" : "扫描已停止";
      $("detail").textContent = `检查 ${status.filesSeen.toLocaleString()} 个文件 · ${status.errors.toLocaleString()} 个项目无权限读取 · ${(status.elapsedMs / 1000).toFixed(1)} 秒`;
      render(status.results || []);
    } else if (status.state === "error") {
      $("status").textContent = "扫描失败";
      toast(status.message);
    }
  } catch (error) { clearInterval(pollTimer); setRunning(false); toast(error.message); }
}

function render(records) {
  $("count").textContent = `找到 ${records.length.toLocaleString()} 个符合条件的文件`;
  if (!records.length) {
    $("rows").innerHTML = `<tr class="empty"><td colspan="5">没有找到符合当前阈值的文件</td></tr>`;
    return;
  }
  $("rows").innerHTML = records.map((item) => `
    <tr data-path="${escapeHTML(item.path)}">
      <td>${humanSize(item.size)}</td>
      <td>${escapeHTML(item.modifiedAt)}</td>
      <td><span class="badge ${item.advice.level}"><span>${escapeHTML(item.advice.label)}</span></span></td>
      <td><div class="path">${escapeHTML(item.path)}</div><div class="reason">${escapeHTML(item.advice.reason)}</div></td>
      <td><button class="trash ghost" ${item.advice.level === "danger" ? "disabled" : ""}>移入回收站</button></td>
    </tr>`).join("");
}

$("rows").addEventListener("click", async (event) => {
  const button = event.target.closest(".trash");
  if (!button) return;
  const row = button.closest("tr");
  const path = row.dataset.path;
  if (!confirm(`确定将此文件移入系统回收站吗？\n\n${path}\n\n文件可从回收站恢复。`)) return;
  button.disabled = true;
  try {
    const result = await api("/api/trash", {
      method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({path})
    });
    row.remove();
    toast(`已移入回收站，释放 ${humanSize(Number(result.released))}`);
  } catch (error) { button.disabled = false; toast(error.message); }
});

$("roots").addEventListener("change", () => {
  const root = roots[Number($("roots").value)];
  if (root) { $("root").value = root.path; showDisk(root); }
});
$("root").addEventListener("input", () => {
  const root = roots.find((item) => item.path === $("root").value);
  showDisk(root);
});
$("scan").addEventListener("click", startScan);
$("cancel").addEventListener("click", () => api("/api/cancel", {method:"POST", headers:{"Content-Type":"application/json"}, body:"{}"}));
$("quit").addEventListener("click", async () => {
  await api("/api/quit", {method:"POST", headers:{"Content-Type":"application/json"}, body:"{}"});
  document.body.innerHTML = `<main><section class="card status-card"><strong>空间卫士已退出，可以关闭此页面。</strong></section></main>`;
});

loadRoots().catch((error) => toast(error.message));
