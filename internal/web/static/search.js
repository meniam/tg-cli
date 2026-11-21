// Two searches over the local database: the whole account from the left
// column, and the open chat from the feed header. Both keep the query, the
// scope and the fetched page so a return restores the same results.
//
// Every input change bumps a generation, and only the newest generation may
// draw. An answer for an older query is dropped even when it arrives late or
// fails late, so clearing the box can never resurrect old results.

import { api, node, clock } from "/api.js";

const RESULT_LIMIT = 60;

function createGuard() {
  let version = 0;
  return { next: () => ++version, current: () => version, cancel: () => ++version };
}

export function createSearch({ state, chatList, onOpenHit, hrefFor }) {
  const globalForm = document.getElementById("global-search");
  const globalQuery = document.getElementById("global-query");
  const globalClear = document.getElementById("global-clear");
  const chatForm = document.getElementById("chat-search");
  const chatQuery = document.getElementById("chat-query");
  const chatScope = document.getElementById("chat-search-scope");
  const chatClear = document.getElementById("chat-search-clear");
  const chatResults = document.getElementById("chat-results");
  const global = createGuard();
  const local = createGuard();

  const hitKey = (hit) => (hit.chat_id ?? hit.scope ?? "") + ":" + hit.id;

  async function runGlobal() {
    const query = globalQuery.value.trim();
    globalClear.hidden = !query;
    if (!query) {
      global.cancel();
      chatList.closeResults();
      return;
    }
    const version = global.next();
    let hits;
    try {
      hits = (await api("search", { query, scopes: ["messages"], limit: RESULT_LIMIT })).hits || [];
    } catch (error) {
      if (version !== global.current()) return; // a stale failure is not news
      if (error.status === 401) throw error;
      chatList.showResults("Search failed", []);
      document.getElementById("chats").replaceChildren(node("p", error.message, "empty warn"));
      return;
    }
    if (version !== global.current()) return;
    state.globalSearch = { query, hits };
    chatList.showResults(
      hits.length >= RESULT_LIMIT ? `${query}: first ${RESULT_LIMIT} matches` : `${query}: ${hits.length} matches`,
      hits.map((hit) => ({
        key: "hit-" + hitKey(hit),
        chat: hit.chat_id ?? state.chat,
        href: hrefFor(hit),
        title: hit.chat_title || "Saved message",
        meta: hit.date ? clock(hit.date) : "",
        snippet: hit.snippet || "(no text)",
        avatar: "⌕",
        open: () => onOpenHit(hit),
      })),
    );
  }

  globalForm.addEventListener("submit", (event) => {
    event.preventDefault();
    void runGlobal();
  });
  globalQuery.addEventListener("input", () => {
    globalClear.hidden = !globalQuery.value.trim();
    // Typing after a request invalidates it, and an empty box drops the results.
    global.cancel();
    if (!globalQuery.value.trim()) chatList.closeResults();
  });
  globalClear.addEventListener("click", () => {
    globalQuery.value = "";
    globalClear.hidden = true;
    global.cancel();
    chatList.closeResults();
    globalQuery.focus();
  });

  function openChatSearch() {
    if (!state.chat) return;
    chatForm.hidden = false;
    chatResults.hidden = !state.localSearch;
    chatScope.textContent = "In " + (state.chatTitle || state.chat);
    chatQuery.focus();
  }

  function closeChatSearch() {
    chatForm.hidden = true;
    chatResults.hidden = true;
    chatQuery.value = "";
    local.cancel();
    state.localSearch = null;
    chatResults.replaceChildren();
    document.getElementById("search-chat").focus();
  }

  function renderChatResults() {
    if (!state.localSearch) {
      chatResults.hidden = true;
      return;
    }
    const { query, hits } = state.localSearch;
    chatResults.hidden = false;
    const scope = hits.length >= RESULT_LIMIT ? `first ${RESULT_LIMIT} matches` : `${hits.length} matches`;
    chatScope.textContent = `In ${state.chatTitle || state.chat} · ${query}: ${scope}`;
    if (!hits.length) {
      chatResults.replaceChildren(node("p", "No saved messages in this chat match the query.", "empty"));
      return;
    }
    chatResults.replaceChildren(...hits.map((hit) => {
      const row = node("button", "", "hit");
      row.type = "button";
      row.dataset.key = hitKey(hit);
      row.append(node("span", hit.snippet || "(no text)", "hit-text"));
      row.append(node("span", hit.date ? clock(hit.date) : "", "hit-meta"));
      row.addEventListener("click", () => onOpenHit(hit));
      return row;
    }));
  }

  async function runChat() {
    const query = chatQuery.value.trim();
    if (!query || !state.chat) return;
    const version = local.next();
    const chat = state.chat;
    let hits;
    try {
      hits = (await api("search", { query, scopes: ["messages"], chat, limit: RESULT_LIMIT })).hits || [];
    } catch (error) {
      if (version !== local.current() || chat !== state.chat) return;
      if (error.status === 401) throw error;
      chatScope.textContent = error.message;
      return;
    }
    if (version !== local.current() || chat !== state.chat) return;
    state.localSearch = { query, chat, hits };
    renderChatResults();
  }

  chatForm.addEventListener("submit", (event) => {
    event.preventDefault();
    void runChat();
  });
  chatClear.addEventListener("click", closeChatSearch);
  document.getElementById("search-chat").addEventListener("click", openChatSearch);

  return {
    openChatSearch,
    closeChatSearch,
    renderChatResults,
    runChat,
    /** The chat input changed: the answer in flight is no longer wanted. */
    invalidateChat: () => local.cancel(),
    /** Leaving a chat drops its local results and cancels a late answer. */
    resetChat() {
      chatForm.hidden = true;
      chatResults.hidden = true;
      chatQuery.value = "";
      state.localSearch = null;
      local.cancel();
      chatResults.replaceChildren();
    },
  };
}
