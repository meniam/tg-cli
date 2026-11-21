// Wiring: the session exchange, routing, the feed lifecycle and the event
// stream. The viewer is read-only: it never sends, edits or marks anything.
//
// Navigation has three rules. The list is always a real entry behind a chat,
// including after a direct link, so Back never leaves the viewer. A jump to one
// message is an entry on top of the live chat view, which is kept and restored
// when Back returns to the plain chat. And every switch of chat or target bumps
// one generation, so a late answer can never draw over a newer selection.

import { api, action, notify, node, initials, avatarTint } from "/api.js";
import { createChatList, messageHref } from "/chatlist.js";
import { createFeed } from "/feed.js";
import { createSearch } from "/search.js";
import { createDetails } from "/details.js";

const byID = (id) => document.getElementById(id);
const OVERVIEW_LIMIT = 500;
const LIST_STATE = { tg: "list" };
const CHAT_STATE = { tg: "chat" };

const state = {
  chats: [],
  folders: [],
  folder: 0,
  chat: "",
  chatTitle: "",
  topic: 0,
  rows: [],
  cursor: "",
  anchor: "",
  // If a chat, topic or target changes, this number changes with it; a response
  // that belongs to an older selection is dropped instead of drawn.
  version: 0,
  // The newest page of the open chat and its scroll offset, kept while a jump
  // shows one narrow window instead.
  liveView: null,
  search: null,
  globalSearch: null,
  localSearch: null,
  pins: [],
  pinIndex: 0,
  // Chats whose pinned preview the reader dismissed in this tab.
  pinHidden: new Set(),
  allowed: () => true,
  jump: () => {},
};

const chatList = createChatList({
  state,
  onOpenChat: (chat) => {
    if (state.search) chatList.closeResults();
    navigate(messageHref(chat), CHAT_STATE);
  },
  onClearSearch: () => {
    state.globalSearch = null;
    byID("global-query").value = "";
    byID("global-clear").hidden = true;
  },
});

const search = createSearch({
  state,
  chatList,
  hrefFor: (hit) => messageHref(hit.chat_id != null ? String(hit.chat_id) : state.chat, String(hit.id)),
  onOpenHit: (hit) => {
    const chat = hit.chat_id != null ? String(hit.chat_id) : state.chat;
    // A hit inside the open chat replaces the live view, so keep it for Back.
    if (chat === state.chat) saveLiveView();
    navigate(messageHref(chat, String(hit.id)), CHAT_STATE);
  },
});

const details = createDetails({ state });

const feed = createFeed({
  onOlder: action(() => loadHistory("", { older: true })),
  onJump: (id) => jumpToMessage(id),
});
state.jump = (id) => jumpToMessage(id);

/**
 * A chat opened by a direct link may be missing from a truncated overview, so
 * the open chat itself is always considered visible; everything else must be
 * present in the policy-filtered overview.
 */
function refreshAllowed() {
  const allowed = new Set(state.chats.map((chat) => String(chat.chat_id)));
  state.allowed = (id) => allowed.has(String(id)) || String(id) === state.chat;
}

async function loadChats() {
  const { chats } = await api("chat.overview", { limit: OVERVIEW_LIMIT });
  state.chats = chats || [];
  refreshAllowed();
  chatList.render();
  chatList.renderFolders();
}

function overviewOf(chat) {
  return state.chats.find((item) => String(item.chat_id) === chat);
}

/**
 * A direct chat link has no entry behind it, so Back would leave the viewer. The
 * list is inserted once as the previous entry; the marker in history.state keeps
 * a reload from inserting it again.
 */
function ownDirectEntry() {
  if (!/^#\/chat\//.test(location.hash) || history.state?.tg === "chat") return;
  // capture first: pushState changes location.href, so reading it afterwards
  // would push the list address and lose the direct chat and its anchor.
  const target = location.pathname + location.hash;
  history.replaceState(LIST_STATE, "", location.pathname);
  history.pushState(CHAT_STATE, "", target);
}

/** Pushes a new address; an unchanged address only re-reads the route. */
function navigate(fragment, navState = CHAT_STATE) {
  if (location.hash === fragment) {
    void readRoute(true);
    return;
  }
  history.pushState(navState, "", fragment || location.pathname);
  void readRoute(true);
}

function showPartial(partial) {
  // A compact affordance in the header, not a bar of its own: the remedy is in
  // the tooltip and in Details, which the badge opens.
  byID("history-badge").hidden = !partial;
  state.historyPartial = partial;
}

function setHeader(chat) {
  const overview = overviewOf(chat);
  state.chatTitle = overview?.title || chat || "";
  const title = state.chatTitle || "Messages";
  byID("chat-title").textContent = title;
  byID("chat-subtitle").textContent = chat ? "Local history" : "Pick a chat on the left";
  const avatar = byID("chat-avatar");
  avatar.textContent = initials(title);
  if (chat) avatar.dataset.tint = avatarTint(chat);
  else delete avatar.dataset.tint;
  byID("open-details").disabled = !chat;
  byID("search-chat").disabled = !chat;
  byID("history-badge").disabled = !chat;
  byID("back").hidden = !chat;
  showPartial(Boolean(chat) && overview?.history_complete !== true);
}

/** Drops everything the previous chat showed, so no stale page can appear. */
function resetConversation(chat) {
  state.version++;
  state.chat = chat;
  state.chatTitle = "";
  state.topic = 0;
  state.rows = [];
  state.cursor = "";
  state.anchor = "";
  state.liveView = null;
  state.pins = [];
  state.pinIndex = 0;
  state.localSearch = null;
  search.resetChat();
  byID("options").hidden = true;
  byID("pin-plate").hidden = true;
  byID("unavailable").hidden = true;
  feed.clear();
  feed.setOlderVisible(false);
  if (chat) feed.draw([], { chat });
}

/** Marks a chat unreadable: its content goes away, the reason stays. */
function showUnavailable(message) {
  state.rows = [];
  state.cursor = "";
  feed.clear();
  feed.setOlderVisible(false);
  const note = byID("unavailable");
  note.hidden = false;
  note.textContent = message;
}

/** Saves the live chat view before a jump replaces it with one window. */
function saveLiveView() {
  if (!state.chat || state.anchor || !state.rows.length) return;
  state.liveView = {
    chat: state.chat,
    topic: state.topic,
    rows: state.rows,
    cursor: state.cursor,
    scroll: feed.scrollTop(),
  };
}

function restoreLiveView(view) {
  state.anchor = "";
  state.rows = view.rows;
  state.cursor = view.cursor;
  feed.draw(view.rows, { mode: "reset", chat: view.chat });
  feed.setOlderVisible(Boolean(view.cursor));
  feed.scrollTo(view.scroll);
  markHighlight("");
  state.liveView = null;
}

// popstate and hashchange both fire for one history move, and either can carry
// an unchanged address: one route key keeps a single move from loading twice.
let routedKey = null;

const readRoute = action(async (force = false) => {
  const key = location.hash + "|" + (history.state?.tg || "");
  if (!force && key === routedKey) return;
  routedKey = key;
  const match = /^#\/chat\/(-?\d+)(?:\/(\d+))?$/.exec(location.hash);
  const chat = match?.[1] || "";
  const anchor = match?.[2] || "";
  const changed = chat !== state.chat;

  if (changed) {
    resetConversation(chat);
  }
  state.anchor = anchor;
  document.body.dataset.screen = chat ? "chat" : "list";
  setHeader(chat);
  chatList.render();

  if (!chat) {
    feed.clear();
    return;
  }
  if (changed) {
    await Promise.all([loadHistory(anchor), loadChatInfo()]);
    return;
  }
  if (anchor) {
    // A jump inside the open chat: an entry on top of the live view.
    state.version++;
    if (feed.scrollToMessage(anchor, "smooth")) {
      markHighlight(anchor);
      return;
    }
    await loadContext(anchor);
    return;
  }
  // Back to the plain chat: the live view is closer than a fresh load.
  const view = state.liveView;
  if (view && view.chat === chat && view.topic === state.topic) {
    restoreLiveView(view);
    return;
  }
  state.version++;
  await loadHistory();
});

async function loadChatInfo() {
  const version = state.version;
  const chat = state.chat;
  let info = null;
  try {
    info = await api("chat.info", { chat });
  } catch (error) {
    if (error.status === 401) throw error;
    if (version !== state.version || chat !== state.chat) return;
    if (error.status === 404 || error.status === 403) {
      showUnavailable("This chat is not in the local database, or the access policy closes it.");
    }
    return;
  }
  if (version !== state.version || chat !== state.chat) return;
  byID("chat-title").textContent = info.title || chat;
  state.chatTitle = info.title || chat;
  byID("chat-avatar").textContent = initials(state.chatTitle);
  const topics = byID("topics");
  if (info.topics?.length) {
    topics.replaceChildren(
      new Option("All topics", "0"),
      ...info.topics.map((topic) => new Option(topic.title || "Topic " + topic.id, topic.id)),
    );
    byID("options").hidden = false;
  }
  state.pins = info.pinned || [];
  state.pinIndex = 0;
  renderPinPlate();
}

/**
 * One pinned preview at a time, as in Web K: segment bars for how many pins the
 * local database holds, the "Pinned Message" label, one ellipsized preview and
 * a control for the rest. The plate is hidden per chat and can be dismissed
 * locally; nothing here writes anywhere.
 */
function renderPinPlate() {
  const plate = byID("pin-plate");
  const pins = state.pins || [];
  const dismissed = state.pinHidden.has(state.chat);
  plate.hidden = !state.chat || !pins.length || dismissed;
  if (plate.hidden) return;

  const index = Math.min(state.pinIndex || 0, pins.length - 1);
  state.pinIndex = index;
  const pin = pins[index];

  // Segment indicators: one bar per stored pin, the active one brighter.
  const bars = byID("pin-bars");
  bars.replaceChildren(...pins.slice(0, 4).map((_, bar) => {
    const segment = node("span", "", "pin-bar");
    if (bar === index) segment.classList.add("is-active");
    return segment;
  }));

  byID("pin-counter").textContent = pins.length > 1 ? `${index + 1}/${pins.length}` : "";
  byID("pin-preview").textContent = pin.text?.replace(/\s+/g, " ").trim()
    || (pin.media_type ? "Media" : "Message " + pin.id);
  byID("pin-all").hidden = pins.length < 2;
  byID("pin-open").setAttribute("aria-label",
    `Pinned message ${index + 1} of ${pins.length}: ${byID("pin-preview").textContent}`);
}

byID("pin-open").addEventListener("click", () => {
  const pins = state.pins || [];
  if (!pins.length) return;
  jumpToMessage(pins[Math.min(state.pinIndex || 0, pins.length - 1)].id);
});
byID("pin-all").addEventListener("click", () => details.open("info"));
byID("pin-hide").addEventListener("click", () => {
  if (!state.chat) return;
  state.pinHidden.add(state.chat);
  renderPinPlate();
  byID("open-details").focus();
});

/** Cycling through pins keeps the plate honest when several are stored. */
function cyclePin(step) {
  const pins = state.pins || [];
  if (pins.length < 2) return;
  state.pinIndex = ((state.pinIndex || 0) + step + pins.length) % pins.length;
  renderPinPlate();
}
byID("pin-plate").addEventListener("keydown", (event) => {
  if (event.key === "ArrowDown" || event.key === "ArrowRight") {
    event.preventDefault();
    cyclePin(1);
  } else if (event.key === "ArrowUp" || event.key === "ArrowLeft") {
    event.preventDefault();
    cyclePin(-1);
  }
});

function sortRows(rows) {
  return [...rows].sort((a, b) => a.id - b.id);
}

/** Deduplicates by id when an older page overlaps what the feed already shows. */
function mergeRows(older, current) {
  const seen = new Set(current.map((row) => row.id));
  return [...older.filter((row) => !seen.has(row.id)), ...current];
}

/** Reads one page of the open chat; a closed chat clears the conversation. */
async function fetchPage(method, params, version, chat) {
  try {
    return await api(method, params);
  } catch (error) {
    if (error.status === 401) throw error;
    if (version !== state.version || chat !== state.chat) return null;
    if (error.status === 404 || error.status === 403) {
      showUnavailable("This chat is not readable now. The access policy may have changed.");
      return null;
    }
    throw error;
  }
}

/**
 * Reloads the window around a message the reader already sees, without
 * marking it: a reconnect or a cursor gap is not a jump to a target.
 */
async function reloadAround(id) {
  if (!id) {
    await loadHistory();
    return;
  }
  const version = state.version;
  const chat = state.chat;
  const result = await fetchPage("message.context", { chat, id: Number(id), before: 20, after: 20 }, version, chat);
  if (!result || version !== state.version || chat !== state.chat) return;
  const rows = sortRows(result.messages || []);
  state.rows = rows;
  state.cursor = result.next_cursor || (rows[0] ? String(rows[0].id) : "");
  feed.draw(rows, { mode: "reset", chat });
  feed.setOlderVisible(Boolean(state.cursor));
  feed.scrollToMessage(String(id));
  markHighlight("");
  if (details.isOpen()) void details.refresh();
}

async function loadHistory(anchor = "", { older = false } = {}) {
  const version = state.version;
  const topic = state.topic;
  const chat = state.chat;
  const result = anchor
    ? await fetchPage("message.context", { chat, id: Number(anchor), before: 20, after: 20 }, version, chat)
    : await fetchPage("message.history", { chat, cursor: older ? state.cursor : "", topic }, version, chat);
  if (!result || version !== state.version || chat !== state.chat || topic !== state.topic) return;
  const rows = sortRows(result.messages || []);
  if (older) {
    const fresh = mergeRows(rows, state.rows);
    if (fresh.length === state.rows.length) {
      // Every row of this page is already drawn: only the cursor moves.
      state.cursor = result.next_cursor || "";
      feed.setOlderVisible(Boolean(state.cursor));
      return;
    }
    state.rows = fresh;
    feed.draw(rows, { mode: "prepend", chat });
  } else {
    state.rows = rows;
    feed.draw(rows, { mode: "reset", chat, anchor });
  }
  state.cursor = result.next_cursor || (anchor && rows[0] ? String(rows[0].id) : "");
  feed.setOlderVisible(Boolean(state.cursor));
  if (anchor) feed.scrollToMessage(String(anchor));
  else if (!older) feed.toBottom();
  if (details.isOpen()) void details.refresh();
}

/** Loads one narrow window around a target and marks it. */
async function loadContext(id) {
  const version = state.version;
  const chat = state.chat;
  const result = await fetchPage("message.context", { chat, id: Number(id), before: 20, after: 20 }, version, chat);
  if (!result || version !== state.version || chat !== state.chat) return;
  const rows = sortRows(result.messages || []);
  state.rows = rows;
  state.cursor = result.next_cursor || (rows[0] ? String(rows[0].id) : "");
  feed.draw(rows, { mode: "reset", chat, anchor: String(id) });
  feed.setOlderVisible(Boolean(state.cursor));
  feed.scrollToMessage(String(id), "smooth");
  if (details.isOpen()) void details.refresh();
}

async function jumpToMessage(id) {
  const target = String(id);
  if (details.isOpen()) details.hide();
  saveLiveView();
  state.anchor = target;
  state.version++;
  if (!location.hash.endsWith("/" + target)) {
    history.pushState(CHAT_STATE, "", messageHref(state.chat, target));
  }
  if (feed.scrollToMessage(target, "smooth")) {
    markHighlight(target);
    return;
  }
  await loadContext(target);
}

function markHighlight(id) {
  for (const card of byID("messages").querySelectorAll(".message.highlight")) {
    card.classList.remove("highlight");
  }
  if (id) byID("msg-" + id)?.classList.add("highlight");
}

byID("topics").addEventListener("change", action(async (event) => {
  // The topic does not change the address, so this is a reload, not a move.
  state.topic = Number(event.target.value);
  state.anchor = "";
  state.liveView = null;
  state.rows = [];
  state.cursor = "";
  state.version++;
  feed.clear();
  history.replaceState(CHAT_STATE, "", messageHref(state.chat));
  await loadHistory();
}));
byID("open-details").addEventListener("click", () => details.open("info"));
byID("history-badge").addEventListener("click", () => details.open("info"));
byID("access").addEventListener("click", () => details.open("access"));
byID("back").addEventListener("click", () => {
  // The list is always an entry behind a chat, including after a direct link.
  if (location.hash) history.back();
  else document.body.dataset.screen = "list";
});
window.addEventListener("popstate", () => { void readRoute(); });
window.addEventListener("hashchange", () => { void readRoute(); });

let searchTimer;
byID("chat-query").addEventListener("input", () => {
  search.invalidateChat();
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => { void search.runChat(); }, 350);
});

/** Server-Sent Events: one row, one card or one reload, never a full redraw. */
function events() {
  let source;
  let reconnect;
  let chatRefresh = 0;
  let stale = false;

  const scheduleChats = () => {
    if (chatRefresh) return;
    chatRefresh = setTimeout(() => {
      chatRefresh = 0;
      void loadChats().catch((error) => {
        if (error.status !== 401) notify(error.message);
      });
    }, 250);
  };

  const countNew = () => {
    const visible = feed.firstVisibleID();
    const index = state.rows.findIndex((row) => String(row.id) === visible);
    return index < 0 ? 1 : Math.max(1, state.rows.length - index - 1);
  };

  const apply = async (event) => {
    const update = JSON.parse(event.data);
    if (!update) return;
    const known = ["message.new", "message.edit", "message.delete", "reaction", "read", "gap"];
    if (!known.includes(update.type)) return;
    scheduleChats();
    if (update.type === "read") return;
    if (!state.chat) return;
    // A jump window is a fixed target; live updates belong to the live view.
    if (state.anchor) return;
    if (update.type === "gap") {
      // A cursor gap means the local window may miss events. Reload the window
      // the reader sees and keep the anchor where it was.
      if (String(update.chat_id ?? "") && String(update.chat_id) !== state.chat) return;
      await reloadAround(feed.firstVisibleID());
      return;
    }
    if (String(update.chat_id) !== state.chat) return;
    const data = update.data || {};
    if (update.type === "message.delete") {
      const ids = data.ids || [];
      state.rows = state.rows.filter((row) => !ids.includes(row.id));
      feed.removeCards(ids);
      return;
    }
    if (update.type === "reaction") {
      const row = state.rows.find((item) => item.id === data.message_id);
      if (!row) return;
      row.reactions = data.reactions || [];
      feed.replaceCard(row, { chat: state.chat, anchor: state.anchor });
      return;
    }
    if (!data.id) return;
    if (state.topic && data.topic_id !== state.topic) return;
    const version = state.version;
    const page = await api("message.context", { chat: state.chat, id: data.id, before: 0, after: 0 });
    if (version !== state.version || !page.messages?.[0]) return;
    const item = page.messages[0];
    const index = state.rows.findIndex((row) => row.id === item.id);
    if (index >= 0) {
      state.rows[index] = item;
      feed.replaceCard(item, { chat: state.chat, anchor: state.anchor });
      return;
    }
    if (update.type !== "message.new") return;
    state.rows.push(item);
    state.rows.sort((a, b) => a.id - b.id);
    const readingBottom = feed.atBottom();
    feed.draw([item], { mode: "append", chat: state.chat });
    if (!readingBottom) feed.showJump(countNew());
  };

  const connect = () => {
    source = new EventSource("/api/events");
    source.onmessage = (event) => {
      void apply(event).catch((error) => {
        if (error.status !== 401) notify(error.message);
      });
    };
    source.onerror = () => {
      if (source.readyState !== EventSource.CLOSED) return;
      if (stale) return;
      stale = true;
      clearTimeout(reconnect);
      reconnect = setTimeout(() => {
        void action(async () => {
          stale = false;
          await loadChats();
          if (state.chat && !state.anchor) await reloadAround(feed.firstVisibleID());
          connect();
        })();
      }, 3000);
    };
    source.onopen = () => { stale = false; };
  };
  connect();
}

const start = action(async () => {
  const token = new URLSearchParams(location.hash.slice(1)).get("token");
  if (token) {
    history.replaceState(null, "", location.pathname);
    const response = await fetch("/api/session", {
      method: "POST",
      headers: { Authorization: "Bearer " + token },
    });
    if (!response.ok) throw new Error("Open a fresh URL printed by tg web.");
  }
  const folders = await api("folder.list");
  state.folders = (folders.folders || []).filter((folder) => folder.id > 0);
  await loadChats();
  if (location.hash) {
    ownDirectEntry();
    await readRoute();
  }
  events();
});

await start();
