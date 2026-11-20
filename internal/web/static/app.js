import { bindSettings } from "/settings.js";

const byID = (id) => document.getElementById(id);
const list = byID("chats");
const timeline = byID("timeline");
const messages = byID("messages");
const searchForm = byID("search");
const panel = byID("panel");
const panelContent = byID("panel-content");
const state = { chats: [], folders: [], folder: 0, chat: "", topic: 0, cursor: "", rows: [], version: 0 };
const dayFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });
const timeFormat = new Intl.DateTimeFormat(undefined, { timeStyle: "short" });

function node(tag, text = "", className = "") {
  const element = document.createElement(tag);
  element.textContent = text;
  if (className) element.className = className;
  return element;
}

function button(text, handler, className = "") {
  const element = node("button", text, className);
  element.type = "button";
  element.addEventListener("click", handler);
  return element;
}

function notify(message) {
  const status = byID("status");
  status.textContent = message;
  status.hidden = !message;
}

async function api(method, params = {}, write = false) {
  const headers = { "Content-Type": "application/json" };
  if (write) headers["X-Tg-Web"] = "settings";
  const response = await fetch("/api/" + method, { method: "POST", headers, body: JSON.stringify(params) });
  if (!response.ok) {
    if (response.status === 401) throw new Error("Session expired. Open the URL printed by tg web.");
    throw new Error((await response.text()).trim() || response.statusText);
  }
  return response.json();
}

function action(fn) {
  return async (...args) => {
    try {
      notify("");
      await fn(...args);
    } catch (error) {
      notify(error.message);
    }
  };
}

function inFolder(chat, folder) {
  if (!folder) return true;
  if (folder.exclude?.some((peer) => peer.id === chat.chat_id)) return false;
  if ([...(folder.pinned || []), ...(folder.include || [])].some((peer) => peer.id === chat.chat_id)) return true;
  const rules = folder.rules;
  if (!rules) return false;
  if (rules.exclude_archived && chat.archived || rules.exclude_muted && chat.muted || rules.exclude_read && !chat.unread && !chat.unread_mark) return false;
  return (chat.type === "user" && (chat.contact ? rules.contacts : rules.non_contacts)) ||
    (chat.type === "bot" && rules.bots) || (chat.type === "channel" && rules.broadcasts) ||
    (["group", "supergroup"].includes(chat.type) && rules.groups);
}

function renderChats() {
  const folder = state.folders.find((item) => item.id === state.folder);
  const folderPins = folder?.pinned?.map((peer) => peer.id) || [];
  const isPinned = (chat) => folder ? folderPins.includes(chat.chat_id) : Boolean(chat.pinned);
  const chats = state.chats.filter((chat) => inFolder(chat, folder));
  chats.sort((a, b) => Number(isPinned(b)) - Number(isPinned(a)) || (b.last_message?.date || 0) - (a.last_message?.date || 0));
  list.replaceChildren(...chats.map((chat) => {
    const title = chat.title || String(chat.chat_id);
    const row = button("", () => route(String(chat.chat_id)), "chat");
    if (String(chat.chat_id) === state.chat) row.setAttribute("aria-current", "true");
    row.append(
      node("span", Array.from(title)[0]?.toLocaleUpperCase() || "?", "avatar"),
      node("strong", title),
      node("small", chat.last_message?.date ? timeFormat.format(chat.last_message.date * 1000) : ""),
      node("span", chat.last_message?.text || chat.type, "preview"),
      node("small", [chat.pinned ? "Pinned" : "", chat.muted ? "Muted" : "", chat.unread || ""].filter(Boolean).join(" · ")),
    );
    return row;
  }));
  if (!chats.length) list.append(node("p", "No local chats in this folder.", "empty"));
}

function renderFolders() {
  byID("folders").replaceChildren(...[{ id: 0, title: "All chats" }, ...state.folders].map((folder) => {
    const tab = button(folder.title, () => {
      state.folder = folder.id;
      renderFolders();
      if (!searchForm.elements.query.value.trim()) renderChats();
    });
    if (state.folder === folder.id) tab.setAttribute("aria-current", "true");
    return tab;
  }));
}

async function loadChats() {
  const { chats } = await api("chat.overview", { limit: 500 });
  state.chats = chats;
  if (!searchForm.elements.query.value.trim()) renderChats();
}

function route(chat, message = "") {
  closePanel();
  const fragment = chat ? "#/chat/" + chat + (message ? "/" + message : "") : "";
  if (location.hash === fragment) {
    void readRoute();
  } else {
    location.hash = fragment;
  }
}

const readRoute = action(async () => {
  const match = /^#\/chat\/(-?\d+)(?:\/(\d+))?$/.exec(location.hash);
  const chat = match?.[1] || "";
  state.chat = chat;
  state.anchor = match?.[2] || "";
  state.topic = 0;
  state.cursor = "";
  state.rows = [];
  state.version++;
  document.body.dataset.screen = chat ? "chat" : "list";
  byID("details").disabled = !chat;
  searchForm.querySelector(".scope").hidden = !chat;
  const overview = state.chats.find((item) => String(item.chat_id) === chat);
  byID("chat-title").textContent = overview?.title || chat || "Messages";
  byID("partial").hidden = !chat || overview?.history_complete === true;
  byID("pinned").hidden = true;
  byID("options").hidden = true;
  renderMessages();
  if (!searchForm.elements.query.value.trim()) renderChats();
  if (!chat) return;
  await Promise.all([loadHistory(match[2]), loadChatInfo()]);
});

async function loadChatInfo() {
  const version = state.version;
  const info = await api("chat.info", { chat: state.chat });
  if (version !== state.version || !info) return;
  byID("chat-title").textContent = info.title || state.chat;
  const topics = byID("topics");
  topics.replaceChildren(new Option("All topics", "0"), ...(info.topics || []).map((topic) => new Option(topic.title, topic.id)));
  byID("options").hidden = !info.topics?.length;
  const pinned = byID("pinned");
  pinned.hidden = !info.pinned?.length;
  pinned.replaceChildren(...(info.pinned || []).map((item) => button(item.text || ("Message " + item.id), () => route(state.chat, item.id))));
}

async function loadHistory(anchor, older = false) {
  const version = state.version;
  const topic = state.topic;
  const height = timeline.scrollHeight;
  const result = anchor
    ? await api("message.context", { chat: state.chat, id: Number(anchor), before: 20, after: 20 })
    : await api("message.history", { chat: state.chat, cursor: older ? state.cursor : "", topic });
  if (version !== state.version || topic !== state.topic) return;
  const rows = [...(result.messages || [])].sort((a, b) => a.id - b.id);
  state.rows = older ? [...rows, ...state.rows] : rows;
  state.cursor = result.next_cursor || (anchor && rows[0] ? String(rows[0].id) : "");
  renderMessages(anchor);
  if (anchor) byID("msg-" + anchor)?.scrollIntoView({ block: "center" });
  else if (older) timeline.scrollTop += timeline.scrollHeight - height;
  else timeline.scrollTop = timeline.scrollHeight;
}

const waiting = new Set();
const thumbnails = new Map();
let downloading = 0;
let pausedUntil = 0;
let pauseTimer;
const observer = new IntersectionObserver((entries) => {
  for (const entry of entries) {
    if (entry.isIntersecting) {
      waiting.add(entry.target);
      const image = entry.target.querySelector("img");
      const url = entry.target.dataset.thumbnail;
      image.src = thumbnails.get(url) || url + "&preview=1";
    } else {
      waiting.delete(entry.target);
    }
  }
  void downloadThumbnails();
}, { root: timeline });

async function downloadThumbnails() {
  if (Date.now() < pausedUntil) {
    clearTimeout(pauseTimer);
    pauseTimer = setTimeout(downloadThumbnails, pausedUntil - Date.now());
    return;
  }
  while (downloading < 3 && waiting.size) {
    const media = waiting.values().next().value;
    waiting.delete(media);
    if (!media.isConnected || thumbnails.has(media.dataset.thumbnail)) continue;
    observer.unobserve(media);
    downloading++;
    const url = media.dataset.thumbnail;
    void (async () => {
      try {
        const response = await fetch(url);
        if (response.status === 429) {
          pausedUntil = Math.max(pausedUntil, Date.now() + 1000 * (Number(response.headers.get("Retry-After")) || 30));
          if (media.isConnected) waiting.add(media);
        } else if (response.ok && media.isConnected) {
          if (thumbnails.size > 250) {
            URL.revokeObjectURL(thumbnails.values().next().value);
            thumbnails.delete(thumbnails.keys().next().value);
          }
          const src = URL.createObjectURL(await response.blob());
          thumbnails.set(url, src);
          media.querySelector("img").src = src;
        } else {
          await response.body?.cancel();
        }
      } catch {
        // The message still shows its media description and embedded preview.
      } finally {
        downloading--;
        void downloadThumbnails();
      }
    })();
  }
}

function renderMessages(anchor = state.anchor) {
  byID("older-row").hidden = !state.cursor;
  observer.disconnect();
  messages.replaceChildren();
  if (!state.rows.length) {
    messages.append(node("p", state.chat ? "No messages in local history." : "Select a chat to view its local history.", "empty"));
    return;
  }
  let previousDay;
  const fragment = document.createDocumentFragment();
  for (const message of state.rows) {
    const date = new Date(message.date);
    const day = dayFormat.format(date);
    if (day !== previousDay) {
      previousDay = day;
      fragment.append(node("div", day, "day"));
    }
    const card = node("article", "", "message" + (message.out ? " out" : ""));
    card.id = "msg-" + message.id;
    if (String(message.id) === anchor) card.classList.add("highlight");
    if (message.sender?.name) card.append(node("strong", message.sender.name, "sender"));
    if (message.forwarded) card.append(node("div", "Forwarded message"));
    if (message.reply_to) {
      const quote = state.rows.find((row) => row.id === message.reply_to);
      card.append(button(quote?.text?.slice(0, 75) || ("Reply to " + message.reply_to), () => route(state.chat, message.reply_to)));
    }
    if (message.text) {
      const paragraph = node("p");
      formatText(paragraph, message.text, message.entities || []);
      card.append(paragraph);
    }
    if (message.media_type) {
      const label = message.media_name || message.media_type;
      const media = node("div", label + (message.media_size ? " · " + Math.ceil(message.media_size / 1024) + " KB" : ""));
      if (["photo", "video", "sticker", "document", "animation", "video_note"].includes(message.media_type)) {
        const preview = node("img");
        preview.alt = message.media_type + " preview";
        preview.hidden = true;
        preview.addEventListener("load", () => { preview.hidden = false; });
        media.dataset.thumbnail = "/api/thumb?chat=" + encodeURIComponent(state.chat) + "&id=" + message.id;
        media.append(preview);
        observer.observe(media);
      }
      card.append(media);
    }
    if (message.service) card.append(node("small", message.service));
    if (message.reactions?.length) card.append(node("small", message.reactions.map((item) => item.emoji + " " + item.count).join("  ")));
    if (message.link) {
      const link = safeLink(message.link, "Open in Telegram");
      if (link) card.append(link);
    }
    const clock = node("time", timeFormat.format(date) + (message.edited ? " · edited" : ""));
    clock.dateTime = message.date;
    card.append(clock);
    fragment.append(card);
  }
  messages.append(fragment);
}

function safeLink(value, text) {
  try {
    const url = new URL(value);
    if (!["http:", "https:", "tg:", "mailto:"].includes(url.protocol)) return null;
    const link = node("a", text);
    link.href = url.href;
    link.rel = "noopener noreferrer";
    link.target = "_blank";
    return link;
  } catch {
    return null;
  }
}

function formatText(container, text, entities) {
  const tags = { bold: "strong", italic: "em", code: "code", pre: "code", blockquote: "q", underline: "u", strike: "s" };
  let offset = 0;
  for (const entity of [...entities].sort((a, b) => a.offset - b.offset)) {
    const end = entity.offset + entity.length;
    if (entity.offset < offset || end > text.length || end <= entity.offset) continue;
    const piece = text.slice(entity.offset, end);
    const formatted = entity.type === "url" || entity.type === "text_url"
      ? safeLink(entity.url || piece, piece)
      : tags[entity.type] ? node(tags[entity.type], piece) : null;
    if (!formatted) continue;
    container.append(text.slice(offset, entity.offset), formatted);
    offset = end;
  }
  container.append(text.slice(offset));
}

let searchVersion = 0;
const search = action(async () => {
  const version = ++searchVersion;
  const query = searchForm.elements.query.value.trim();
  if (!query) {
    renderChats();
    return;
  }
  const result = await api("search", { query, scopes: ["messages"], chat: searchForm.elements.local.checked ? state.chat : "", limit: 60 });
  if (version !== searchVersion) return;
  list.replaceChildren(...(result.hits || []).map((hit) => {
    const item = button("", () => route(String(hit.chat_id), hit.id), "chat");
    item.append(node("span", "↗", "avatar"), node("strong", hit.chat_title || String(hit.chat_id)), node("small"), node("span", hit.snippet, "preview"));
    return item;
  }));
  if (!list.childElementCount) list.append(node("p", "No matching messages.", "empty"));
});

searchForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void search();
});
searchForm.elements.query.addEventListener("input", () => {
  if (!searchForm.elements.query.value.trim()) void search();
});
byID("back").addEventListener("click", () => { document.body.dataset.screen = "list"; });
byID("older").addEventListener("click", action(async () => {
  byID("older").disabled = true;
  try { await loadHistory("", true); }
  finally { byID("older").disabled = false; }
}));
byID("topics").addEventListener("change", action(async (event) => {
  state.topic = Number(event.target.value);
  state.anchor = "";
  await loadHistory();
}));

function openPanel(title) {
  byID("panel-title").textContent = title;
  panelContent.replaceChildren();
  panel.hidden = false;
  document.body.dataset.screen = "panel";
}

function closePanel() {
  panel.hidden = true;
  document.body.dataset.screen = state.chat ? "chat" : "list";
}

byID("close").addEventListener("click", closePanel);
byID("details").addEventListener("click", action(async () => {
  openPanel("Chat details");
  const info = await api("chat.info", { chat: state.chat });
  panelContent.append(node("h2", info.title));
  if (info.username) panelContent.append(node("p", "@" + info.username));
  if (info.member_count != null) panelContent.append(node("p", info.member_count + " members"));
  if (info.about) panelContent.append(node("p", info.about));
  await showChatSettings();
}));
byID("access").addEventListener("click", action(showAccess));

function events() {
  let source;
  let reconnect;
  const refresh = async (event) => {
    const update = JSON.parse(event.data);
    if (!update) return;
    if (!["message.new", "message.edit", "message.delete", "reaction", "read", "gap"].includes(update.type)) return;
    await loadChats();
    if (update.type === "gap" && state.chat) {
      await loadHistory();
      return;
    }
    if (!state.chat || String(update.chat_id) !== state.chat) return;
    const version = state.version;
    const atBottom = timeline.scrollHeight - timeline.scrollTop - timeline.clientHeight < 80;
    const data = update.data || {};
    switch (update.type) {
      case "message.new":
      case "message.edit": {
        if (!data.id || (state.topic && data.topic_id !== state.topic)) break;
        const page = await api("message.context", { chat: state.chat, id: data.id, before: 0, after: 0 });
        if (version !== state.version || !page.messages?.[0]) return;
        const item = page.messages[0];
        const index = state.rows.findIndex((row) => row.id === item.id);
        if (index >= 0) state.rows[index] = item;
        else if (update.type === "message.new") state.rows.push(item);
        break;
      }
      case "message.delete":
        state.rows = state.rows.filter((row) => !data.ids?.includes(row.id));
        break;
      case "reaction": {
        const row = state.rows.find((item) => item.id === data.message_id);
        if (row) row.reactions = data.reactions;
        break;
      }
      default:
        return;
    }
    renderMessages();
    if (atBottom && !state.anchor) timeline.scrollTop = timeline.scrollHeight;
  };
  const connect = () => {
    source = new EventSource("/api/events");
    source.onmessage = (event) => { void refresh(event).catch((error) => notify(error.message)); };
    source.onerror = () => {
      if (source.readyState !== EventSource.CLOSED) return;
      clearTimeout(reconnect);
      reconnect = setTimeout(action(async () => {
        await loadChats();
        if (state.chat) await loadHistory();
        connect();
      }), 3000);
    };
  };
  connect();
}

async function showAccess() {
  openPanel("Access");
  panelContent.append(node("p", "Access settings are loading…"));
  await renderAccessSettings();
}

const { renderAccessSettings, showChatSettings } = bindSettings({
  api, node, button, action, notify, panelContent, state, loadChats, renderFolders, renderChats,
});

const start = action(async () => {
  const token = new URLSearchParams(location.hash.slice(1)).get("token");
  if (token) {
    history.replaceState(null, "", location.pathname);
    const response = await fetch("/api/session", { method: "POST", headers: { Authorization: "Bearer " + token } });
    if (!response.ok) throw new Error("Open a fresh URL from tg web.");
  }
  const [folders] = await Promise.all([api("folder.list"), loadChats()]);
  state.folders = (folders.folders || []).filter((folder) => folder.id > 0);
  renderFolders();
  window.addEventListener("hashchange", readRoute);
  if (location.hash) await readRoute();
  events();
});

await start();
