const byId = (id) => document.getElementById(id)
const storageKey = "labnana2api-admin-key"

function getApiKey() {
  return byId("adminApiKey").value.trim()
}

function authHeaders() {
  const apiKey = getApiKey()
  return apiKey ? { Authorization: `Bearer ${apiKey}` } : {}
}

function setStatus(id, message, isError = false) {
  const el = byId(id)
  el.textContent = message
  el.classList.add("show")
  el.classList.toggle("error", isError)
}

function clearStatus(id) {
  const el = byId(id)
  el.textContent = ""
  el.classList.remove("show", "error")
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[char]))
}

// 只允许 http/https 或当前站点相对地址，避免把不可信字符串直接塞进 src/href。
function sanitizeURL(value, fallback = "#") {
  if (!value) return fallback
  try {
    const parsed = new URL(value, window.location.origin)
    const allowed = parsed.protocol === "http:" || parsed.protocol === "https:"
    return allowed ? parsed.toString() : fallback
  } catch (_) {
    return fallback
  }
}

async function fetchJSON(url, options = {}) {
  const headers = {
    ...(options.headers || {}),
    ...authHeaders(),
  }
  const response = await fetch(url, { ...options, headers })
  const data = await response.json().catch(() => ({}))
  if (!response.ok) {
    throw new Error(data?.error?.message || data?.message || `HTTP ${response.status}`)
  }
  return data
}

function renderEmpty(containerId, message) {
  byId(containerId).innerHTML = `<div class="empty-state">${escapeHTML(message)}</div>`
}

function formatTime(value) {
  if (!value) return "-"
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? "-" : time.toLocaleString()
}

// 统计卡片走无鉴权接口，保证页面首屏至少能看到运行概况。
async function loadTelemetry() {
  const data = await fetchJSON("/api/telemetry")
  byId("statusText").textContent = data.status || "unknown"
  byId("keyCount").textContent = String(data.keys_enabled || 0)
  byId("galleryCount").textContent = String(data.gallery_items || 0)
  byId("storageText").textContent = data.object_storage?.enabled ? "enabled" : "disabled"
  byId("modelBadge").textContent = `${data.labnana?.default_model || "gpt-image-2"} · ${data.labnana?.image_size || "2K"}`
  clearStatus("pageStatus")
}

async function loadConfig() {
  try {
    const data = await fetchJSON("/api/config")
    byId("configDump").textContent = JSON.stringify(data, null, 2)
    clearStatus("configStatus")
  } catch (error) {
    setStatus("configStatus", error.message, true)
  }
}

function renderKeyItem(item) {
  const errorHTML = item.last_error
    ? `<div class="muted" style="color:#b91c1c;">最近错误: ${escapeHTML(item.last_error)}</div>`
    : ""
  return `
    <article class="key-row">
      <div class="key-header">
        <div class="key-meta">
          <div class="key-name">${escapeHTML(item.name)}</div>
          <div class="muted mono">${escapeHTML(item.masked_key || "")}</div>
        </div>
        <span class="badge">${item.enabled ? "enabled" : "disabled"}</span>
      </div>
      <div class="key-summary">
        <span class="muted">成功 ${item.success_count || 0} · 失败 ${item.failure_count || 0}</span>
        <span class="muted">最近使用 ${escapeHTML(formatTime(item.last_used_at))}</span>
        ${errorHTML}
      </div>
      <div class="key-actions">
        <button class="button button-secondary" data-action="toggle" data-name="${escapeHTML(item.name)}" data-enabled="${item.enabled}">
          ${item.enabled ? "停用" : "启用"}
        </button>
        <button class="button button-secondary" data-action="check" data-name="${escapeHTML(item.name)}">检查</button>
        <button class="button button-danger" data-action="delete" data-name="${escapeHTML(item.name)}">删除</button>
      </div>
    </article>
  `
}

async function loadKeys() {
  try {
    const data = await fetchJSON("/api/keys")
    const items = data.keys || []
    if (items.length === 0) {
      renderEmpty("keyList", "还没有可展示的 Key。")
      clearStatus("keyListStatus")
      return
    }
    byId("keyList").innerHTML = items.map(renderKeyItem).join("")
    clearStatus("keyListStatus")
  } catch (error) {
    setStatus("keyListStatus", error.message, true)
  }
}

function renderGalleryItem(item) {
  const imageURL = sanitizeURL(item.url, "")
  const linkURL = sanitizeURL(item.url, "#")
  const prompt = escapeHTML(item.prompt || "(empty prompt)")
  const meta = [
    item.model || "gpt-image-2",
    item.size || "2K",
    item.aspect_ratio || "1:1",
    `refs ${item.reference_count || 0}`,
  ].map(escapeHTML).join(" · ")
  return `
    <article class="gallery-card">
      <img src="${imageURL}" alt="${prompt}" loading="lazy" />
      <div class="gallery-meta">
        <div class="gallery-title">${prompt}</div>
        <div class="muted">${meta}</div>
        <div class="muted">${escapeHTML(formatTime(item.created_at))}</div>
        <a class="gallery-link" href="${linkURL}" target="_blank" rel="noreferrer">${escapeHTML(item.url || "")}</a>
      </div>
    </article>
  `
}

async function loadGallery() {
  try {
    const data = await fetchJSON("/api/gallery?limit=50")
    const items = data.items || []
    if (items.length === 0) {
      renderEmpty("galleryList", "还没有图片生成记录。")
      clearStatus("galleryStatus")
      return
    }
    byId("galleryList").innerHTML = items.map(renderGalleryItem).join("")
    clearStatus("galleryStatus")
  } catch (error) {
    setStatus("galleryStatus", error.message, true)
  }
}

async function addKey() {
  const name = byId("keyName").value.trim()
  const key = byId("keyValue").value.trim()
  if (!name || !key) {
    setStatus("keyFormStatus", "名称和 Key 都不能为空", true)
    return
  }
  try {
    await fetchJSON("/api/keys", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, key, enabled: true }),
    })
    byId("keyName").value = ""
    byId("keyValue").value = ""
    setStatus("keyFormStatus", "Key 已添加")
    await Promise.all([loadTelemetry(), loadKeys()])
  } catch (error) {
    setStatus("keyFormStatus", error.message, true)
  }
}

async function handleKeyAction(event) {
  const button = event.target.closest("button[data-action]")
  if (!button) return
  const name = button.dataset.name
  const action = button.dataset.action
  try {
    if (action === "toggle") {
      const enabled = button.dataset.enabled !== "true"
      await fetchJSON(`/api/keys/${encodeURIComponent(name)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
      })
    }
    if (action === "delete") {
      await fetchJSON(`/api/keys/${encodeURIComponent(name)}`, { method: "DELETE" })
    }
    if (action === "check") {
      await fetchJSON(`/api/keys/${encodeURIComponent(name)}/check`, { method: "POST" })
      setStatus("keyListStatus", `Key ${name} 检查通过`)
    }
    await Promise.all([loadTelemetry(), loadKeys()])
  } catch (error) {
    setStatus("keyListStatus", error.message, true)
  }
}

function restoreApiKey() {
  const saved = localStorage.getItem(storageKey)
  if (saved) {
    byId("adminApiKey").value = saved
  }
}

function saveApiKey() {
  localStorage.setItem(storageKey, getApiKey())
  setStatus("authStatus", "浏览器本地已保存 API Key")
}

async function refreshAll() {
  const results = await Promise.allSettled([loadTelemetry(), loadConfig(), loadKeys(), loadGallery()])
  const hasTelemetryError = results[0]?.status === "rejected"
  if (hasTelemetryError) {
    setStatus("pageStatus", results[0].reason?.message || "面板刷新失败", true)
  }
}

byId("saveApiKey").addEventListener("click", saveApiKey)
byId("addKey").addEventListener("click", addKey)
byId("refreshAll").addEventListener("click", refreshAll)
byId("keyList").addEventListener("click", handleKeyAction)

restoreApiKey()
refreshAll()
