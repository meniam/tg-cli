// The left column: folders, the chat list and the search results that replace
// it. Rows are real links keyed by chat id, so an update touches only the rows
// that changed and never drops keyboard focus.
//
// Every value here comes from a field the local overview already carries. Web K
// shows stories, real photos, presence and "New chat" actions; the local RPC has
// none of them, so no placeholder stands in for them.

import { node, button, shortDate, avatarTint, applyTint, initials } from "/api.js";

const OVERVIEW_LIMIT = 500;

/** Applies the locally saved folder rules to one overview row. */
export function inFolder(chat, folder) {
  if (!folder) return true;
  if (folder.exclude?.some((peer) => peer.id === chat.chat_id)) return false;
  if ([...(folder.pinned || []), ...(folder.include || [])].some((peer) => peer.id === chat.chat_id)) return true;
  const rules = folder.rules;
  if (!rules) return false;
  if (rules.exclude_archived && chat.archived) return false;
  if (rules.exclude_muted && chat.muted) return false;
  if (rules.exclude_read && !chat.unread && !chat.unread_mark) return false;
  return (
    (chat.type === "user" && (chat.contact ? rules.contacts : rules.non_contacts)) ||
    (chat.type === "bot" && rules.bots) ||
    (chat.type === "channel" && rules.broadcasts) ||
    (["group", "supergroup"].includes(chat.type) && rules.groups)
  );
}

/** Text a chat row shows when its last saved message has no text of its own. */
export function previewText(chat) {
  const last = chat.last_message;
  if (!last) return chat.type === "user" ? "No saved messages" : chat.type;
  if (last.text) return last.text.replace(/\s+/g, " ").trim();
  return "Media or service message";
}

export function messageHref(chat, message) {
  return "#/chat/" + chat + (message ? "/" + message : "");
}

const svgNS = "http://www.w3.org/2000/svg";

function glyph(className, paths, viewBox = "0 0 20 20") {
  const icon = document.createElementNS(svgNS, "svg");
  icon.setAttribute("viewBox", viewBox);
  icon.setAttribute("aria-hidden", "true");
  icon.setAttribute("focusable", "false");
  icon.setAttribute("class", className);
  for (const [name, attrs] of paths) {
    const part = document.createElementNS(svgNS, name);
    for (const [key, value] of Object.entries(attrs)) part.setAttribute(key, value);
    icon.append(part);
  }
  return icon;
}

/** A muted chat keeps a monochrome marker beside its title, as Web K does. */
function muteGlyph() {
  return glyph("mute-glyph", [
    ["path", { d: "M4 8h3l4-3v10l-4-3H4z", fill: "currentColor" }],
    ["path", { d: "M13.5 8.5l3 3m0-3l-3 3", stroke: "currentColor", "stroke-width": "1.6", "stroke-linecap": "round" }],
  ]);
}

/** A pinned chat wears a small capsule beside its date. */
function pinGlyph() {
  return glyph("pin-glyph", [
    ["path", { d: "M11.5 3.5 16.5 8.5M13.8 6.2 9.5 10.5 6.5 13.5M4.5 15.5 8 12", stroke: "currentColor", "stroke-width": "1.6", "stroke-linecap": "round" }],
  ]);
}

export function createChatList({ state, onOpenChat, onClearSearch }) {
  const list = document.getElementById("chats");
  const folders = document.getElementById("folders");
  const listHead = document.getElementById("list-head");
  const listHeadTitle = document.getElementById("list-head-title");
  const rows = new Map();
  const boundary = node("p", "", "boundary");
  boundary.hidden = true;
  boundary.textContent = `Showing the newest ${OVERVIEW_LIMIT} saved chats. Folder counts are hidden because the local snapshot is truncated.`;

  /** True when the local overview hit its own row limit, so counts are partial. */
  function truncated() {
    return state.chats.length >= OVERVIEW_LIMIT;
  }

  /** The chats a folder holds, from the local snapshot only. */
  function chatsIn(folderId) {
    if (folderId === "archive") {
      return state.chats.filter((chat) => chat.archived && state.allowed(chat.chat_id));
    }
    const folder = state.folders.find((item) => item.id === folderId);
    return state.chats.filter((chat) => inFolder(chat, folder) && state.allowed(chat.chat_id));
  }

  /** A custom folder pins its own peers; otherwise the dialog pin decides. */
  function isPinned(chat) {
    const folder = state.folders.find((item) => item.id === state.folder);
    if (!folder) return Boolean(chat.pinned);
    const folderPins = folder.pinned?.map((peer) => peer.id) || [];
    return folderPins.includes(chat.chat_id);
  }

  function visibleChats() {
    return chatsIn(state.folder).sort((a, b) => Number(isPinned(b)) - Number(isPinned(a)) ||
      (b.last_message?.date || 0) - (a.last_message?.date || 0));
  }

  function rowFor(chat) {
    const id = String(chat.chat_id);
    const row = document.createElement("a");
    row.className = "chat";
    row.href = messageHref(id);
    row.dataset.chat = id;
    row.append(node("span", "", "avatar"), node("span", "", "chat-body"));
    row.querySelector(".chat-body").append(
      node("span", "", "chat-title"),
      node("span", "", "chat-preview"),
    );
    const side = node("span", "", "chat-side");
    side.append(node("span", "", "chat-dates"), node("span", "", "chat-meta"));
    side.querySelector(".chat-dates").append(node("span", "", "chat-time"), node("span", "", "chat-pin"));
    row.append(side);
    row.addEventListener("click", (event) => {
      if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey) return;
      event.preventDefault();
      onOpenChat(id);
    });
    fillRow(row, chat);
    return row;
  }

  /** Updates an existing row in place: same nodes, same focus, new values. */
  function fillRow(row, chat) {
    const title = chat.title || String(chat.chat_id);
    const avatar = row.querySelector(".avatar");
    avatar.textContent = initials(title);
    avatar.dataset.tint = avatarTint(chat.chat_id);

    // The mute marker follows the title text; the count gets its own pill below.
    const titleNode = row.querySelector(".chat-title");
    titleNode.replaceChildren(document.createTextNode(title), ...(chat.muted ? [muteGlyph()] : []));
    row.querySelector(".chat-preview").textContent = previewText(chat);
    row.querySelector(".chat-time").textContent = shortDate(chat.last_message?.date);

    // Pin is a small capsule beside the date, shown when this row is pinned in
    // the view being read: a custom folder's own pin list wins over the chat's
    // dialog pin, the same rule that orders the rows.
    const pin = row.querySelector(".chat-pin");
    const pinned = isPinned(chat);
    if (pinned && !pin.firstChild) pin.append(pinGlyph());
    pin.hidden = !pinned;
    pin.title = pinned ? "Pinned" : "";

    const count = chat.unread ? String(chat.unread) : "";
    const meta = row.querySelector(".chat-meta");
    meta.textContent = count;
    meta.hidden = !count;

    row.classList.toggle("unread", Boolean(chat.unread) || Boolean(chat.unread_mark));
    row.classList.toggle("muted", Boolean(chat.muted));
    row.setAttribute("aria-label", [title, previewText(chat), rowMeta(chat)].filter(Boolean).join(", "));
    if (String(chat.chat_id) === state.chat) row.setAttribute("aria-current", "true");
    else row.removeAttribute("aria-current");
  }

  /** The row's accessible name: the visible text plus its state words. */
  function rowMeta(chat) {
    const parts = [];
    if (chat.pinned) parts.push("pinned");
    if (chat.muted) parts.push("muted");
    if (chat.archived) parts.push("archived");
    if (chat.unread) parts.push(chat.unread + " unread");
    return parts.join(", ");
  }

  function order() {
    return [...list.children]
      .filter((child) => child.classList.contains("chat"))
      .map((child) => child.dataset.chat);
  }

  /**
   * Reconciles the list by chat id. Rows that keep their place are patched and
   * never detached, so the keyboard focus and the scroll position survive an
   * event refresh; only rows that really moved are reinserted.
   */
  function render() {
    if (state.search) return; // results own the list until they are closed
    listHead.hidden = true;
    folders.hidden = false;
    const chats = visibleChats();
    list.querySelector(".empty")?.remove();
    list.classList.toggle("empty", chats.length === 0);
    if (!chats.length) {
      rows.clear();
      list.replaceChildren(node("p", emptyText(), "empty"));
      return;
    }
    const ids = chats.map((chat) => String(chat.chat_id));
    const keep = new Set(ids);
    for (const [id, row] of rows) {
      if (!keep.has(id)) {
        row.remove();
        rows.delete(id);
      }
    }
    for (const chat of chats) {
      const id = String(chat.chat_id);
      const existing = rows.get(id);
      if (existing) fillRow(existing, chat);
      else rows.set(id, rowFor(chat));
    }
    if (order().join("\n") !== ids.join("\n")) reorder(ids);
    boundary.hidden = !truncated();
    if (boundary.parentElement !== list) list.append(boundary);
  }

  /** Moves only the rows whose position is wrong, comparing against the target. */
  function reorder(ids) {
    let cursor = list.firstElementChild;
    for (const id of ids) {
      const row = rows.get(id);
      if (!row) continue;
      if (row === cursor) {
        cursor = cursor.nextElementSibling;
        continue;
      }
      list.insertBefore(row, cursor);
    }
  }

  function emptyText() {
    if (!state.chats.length) return "No local chats yet. Run tg chat list to save dialogs.";
    if (state.folder === "archive") return "No archived chats in the local snapshot.";
    return "No local chats in this folder.";
  }

  /**
   * A folder tab carries the number of unread chats it holds, not a sum of
   * message counts: Web K's badge counts conversations. It appears only when the
   * local snapshot holds every chat, since a truncated overview would understate it.
   */
  function renderFolders() {
    const tabs = [{ id: 0, title: "All chats" }, ...state.folders.filter((folder) => folder.id > 0)];
    if (state.chats.some((chat) => chat.archived)) tabs.push({ id: "archive", title: "Archived" });
    const complete = !truncated();
    folders.replaceChildren(...tabs.map((folder) => {
      const tab = button("", () => selectFolder(folder.id), "tab");
      tab.setAttribute("role", "tab");
      tab.append(node("span", folder.title, "tab-title"));
      const unread = complete ? unreadChats(folder.id) : 0;
      if (unread) tab.append(node("span", String(unread), "tab-count"));
      if (state.folder === folder.id) tab.setAttribute("aria-selected", "true");
      tab.setAttribute("aria-label", unread ? `${folder.title}, ${unread} unread chats` : folder.title);
      return tab;
    }));
    folders.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }

  function unreadChats(folderId) {
    return chatsIn(folderId).filter((chat) => chat.unread || chat.unread_mark).length;
  }

  function selectFolder(id) {
    state.folder = id;
    renderFolders();
    render();
  }

  /** Search results also open by a real address, so they stay links. */
  function showResults(title, items) {
    state.search = items;
    listHead.hidden = false;
    listHeadTitle.textContent = title;
    folders.hidden = true;
    rows.clear();
    list.classList.toggle("empty", items.length === 0);
    if (!items.length) {
      list.replaceChildren(node("p", "No saved messages match this search.", "empty"));
      return;
    }
    const fragment = document.createDocumentFragment();
    for (const hit of items) {
      const card = document.createElement("a");
      card.className = "chat result";
      card.href = hit.href;
      card.dataset.chat = String(hit.chat);
      const body = node("span", "", "chat-body");
      body.append(node("span", hit.title, "chat-title"), node("span", hit.snippet, "chat-preview"));
      const side = node("span", "", "chat-side");
      side.append(node("span", hit.meta || "", "chat-time"));
      card.append(
        applyTint(node("span", hit.avatar || "\u2315", "avatar"), hit.key),
        body,
        side,
      );
      card.addEventListener("click", (event) => {
        if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey) return;
        event.preventDefault();
        hit.open();
      });
      fragment.append(card);
    }
    list.replaceChildren(fragment);
  }

  function closeResults() {
    if (!state.search) return;
    state.search = null;
    onClearSearch();
    renderFolders();
    render();
  }

  document.getElementById("search-back").addEventListener("click", closeResults);

  return { render, renderFolders, showResults, closeResults, selectFolder, truncated };
}
