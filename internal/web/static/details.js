// The right panel: read-only chat details, the attachments that are actually
// loaded in the feed, and the access policy. Nothing here changes anything.

import { api, node, clock } from "/api.js";

const mediaKinds = {
  photo: "Photos",
  video: "Video",
  animation: "Animations",
  document: "Files",
  voice: "Voice messages",
  video_note: "Video messages",
  sticker: "Stickers",
  audio: "Audio",
};

export function createDetails({ state }) {
  const panel = document.getElementById("panel");
  const content = document.getElementById("panel-content");
  const tabs = document.getElementById("panel-tabs");
  const close = document.getElementById("close-panel");
  let currentTab = "info";

  function activeTab() {
    return currentTab;
  }

  /**
   * Web K slides a side panel in over its content with a short slide plus a
   * fade (slide-fade, --transition-standard-in = 300ms) and takes it away on the
   * out timing (250ms). The panel keeps its own transform/opacity state so the
   * transition really runs; `hidden` is applied only after the exit finishes.
   */
  const ENTER_MS = 300;
  const EXIT_MS = 250;

  function reducedMotion() {
    return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  }

  function animate(entering) {
    const timing = { duration: reducedMotion() ? 0 : (entering ? ENTER_MS : EXIT_MS), easing: "cubic-bezier(.4, 0, .2, 1)", fill: "both" };
    // Reduced motion fades in place; otherwise the panel travels in from its
    // own edge, which is what Web K's slide-fade does.
    const from = entering
      ? [{ transform: reducedMotion() ? "none" : "translateX(1.5rem)", opacity: 0 },
        { transform: "translateX(0)", opacity: 1 }]
      : [{ transform: "translateX(0)", opacity: 1 },
        { transform: reducedMotion() ? "none" : "translateX(1.5rem)", opacity: 0 }];
    panel.dataset.state = entering ? "open" : "closed";
    const animation = panel.animate(from, timing);
    if (entering) {
      // Cancel once finished: fill keeps it visible in the meantime, and a
      // lingering entry must not be mistaken for the exit that follows it.
      animation.addEventListener("finish", () => {
        if (panel.dataset.state === "open") animation.cancel();
      }, { once: true });
    }
    return animation;
  }

  let exit;
  function open(tab = "info") {
    exit?.cancel();
    exit = undefined;
    panel.hidden = false;
    document.body.dataset.screen = "panel";
    animate(true);
    void select(tab);
  }

  function hide() {
    if (panel.hidden) return;
    document.body.dataset.screen = state.chat ? "chat" : "list";
    if (exit) return;
    const animation = animate(false);
    exit = animation;
    const done = () => {
      exit = undefined;
      // Only hide if nothing reopened the panel while it was leaving.
      if (panel.dataset.state === "closed" && !panel.hidden) panel.hidden = true;
    };
    animation.addEventListener("finish", done, { once: true });
    animation.addEventListener("cancel", () => { exit = undefined; }, { once: true });
  }

  async function select(tab) {
    currentTab = tab;
    for (const button of tabs.querySelectorAll("button")) {
      button.setAttribute("aria-selected", String(button.dataset.tab === tab));
    }
    content.replaceChildren(node("p", "Loading local details…", "muted"));
    try {
      if (tab === "info") await renderInfo();
      else if (tab === "attachments") renderAttachments();
      else await renderAccess();
    } catch (error) {
      if (error.status === 401) throw error;
      content.replaceChildren(node("p", "Local details are unavailable: " + error.message, "empty warn"));
    }
  }

  async function renderInfo() {
    if (!state.chat) {
      content.replaceChildren(node("p", "Select a chat to see its local details.", "empty"));
      return;
    }
    const chat = state.chat;
    const info = await api("chat.info", { chat });
    if (chat !== state.chat || !info) return;
    const overview = state.chats.find((item) => String(item.chat_id) === chat);
    const section = node("section");
    section.append(node("h2", info.title || chat));
    const facts = node("dl");
    addFact(facts, "Type", overview?.type || "");
    addFact(facts, "Username", info.username ? "@" + info.username : "");
    addFact(facts, "Members", info.member_count != null ? String(info.member_count) : "");
    addFact(facts, "Saved messages", overview ? String(overview.messages) : "");
    addFact(facts, "History", overview?.history_complete === true ? "Complete" : "Partial, local only");
    section.append(facts);
    if (info.about) section.append(node("p", info.about, "about"));
    content.replaceChildren(section);

    if (info.pinned?.length) {
      // The full list of locally stored pins; the plate in the feed shows one.
      const pinned = node("section");
      // The local database holds at most 10 pins, so this is not a claim about
      // every pin in Telegram.
      pinned.append(node("h2", `Pinned messages · ${info.pinned.length} stored locally`));
      for (const item of info.pinned) {
        const row = node("button", "", "pinned-row");
        row.type = "button";
        row.append(node("span", item.text?.replace(/\s+/g, " ").trim()
          || (item.media_type ? "Media" : "Message " + item.id), "pinned-row-text"));
        row.append(node("span", clock(item.date), "pinned-row-time"));
        row.addEventListener("click", () => state.jump(item.id));
        pinned.append(row);
      }
      content.append(pinned);
    }
    // An incomplete local history stays discoverable where the badge points.
    if (state.historyPartial) {
      const history = node("section");
      history.append(node("h2", "Local history"));
      history.append(node("p", "Older messages are not saved locally. Run tg chat sync to load more.", "scope-note"));
      content.append(history);
    }
    if (info.topics?.length) {
      const topics = node("section");
      topics.append(node("h2", "Topics in local history"));
      const list = node("ul", "", "plain");
      for (const topic of info.topics) {
        const item = node("li", topic.title || "Topic " + topic.id);
        if (topic.id === state.topic) item.classList.add("current");
        list.append(item);
      }
      topics.append(list);
      content.append(topics);
    }
    const settings = await api("chat.settings", { chat }).catch(() => null);
    if (chat !== state.chat) return;
    if (settings?.history_mode) {
      const mode = node("section");
      mode.append(node("h2", "Local history mode"));
      mode.append(node("p", describeMode(settings.history_mode), "muted"));
      content.append(mode);
    }
  }

  function describeMode(mode) {
    if (mode === "skip") return "Background history skips this chat.";
    if (mode === "first") return "Background history takes this chat first.";
    return "Background history follows the default order.";
  }

  function addFact(dl, label, value) {
    if (!value) return;
    dl.append(node("dt", label), node("dd", value));
  }

  /**
   * Attachments come from the messages loaded in the feed, not from the whole
   * local history, so the scope is stated and the counts are exact for that
   * window only.
   */
  function renderAttachments() {
    const loaded = state.rows.filter((row) => row.media_type);
    const section = node("section");
    section.append(node("h2", "Attachments"));
    section.append(node("p",
      `From ${state.rows.length} messages loaded in this feed, not from the whole local history.`,
      "scope-note"));
    if (!loaded.length) {
      section.append(node("p", "No media in the loaded part of this chat.", "empty"));
      content.replaceChildren(section);
      return;
    }
    const byKind = new Map();
    for (const row of loaded) {
      const kind = mediaKinds[row.media_type] || "Other";
      byKind.set(kind, [...(byKind.get(kind) || []), row]);
    }
    for (const [kind, rows] of byKind) {
      const group = node("section");
      group.append(node("h3", `${kind} · ${rows.length}`));
      for (const row of rows) {
        const item = node("button", "", "attachment");
        item.type = "button";
        item.append(node("span", row.media_name || row.media_type, "attachment-name"));
        item.append(node("span", clock(row.date), "attachment-meta"));
        item.addEventListener("click", () => state.jump(row.id));
        group.append(item);
      }
      section.append(group);
    }
    content.replaceChildren(section);
  }

  async function renderAccess() {
    const policy = await api("access.view");
    const section = node("section");
    section.append(node("h2", "Access policy"));
    section.append(node("p", "Read-only view of the profile policy. Edit it outside the browser.", "scope-note"));
    if (policy.closed) {
      section.append(node("p", "The policy file is unavailable, so every chat is closed.", "warn"));
    }
    const groups = [
      ["Chat lists", [["allow", "Allow list"], ["block", "Block list"], ["write", "Message writes"], ["admin", "Chat administration"]]],
      ["Account permissions", [["dialogs", "Manage dialogs and folders"], ["sessions", "Terminate sessions"], ["create", "Create chats"], ["profile", "Edit profile"]]],
      ["Directories", [["files", "Send files from"], ["downloads", "Download files to"]]],
    ];
    for (const [title, fields] of groups) {
      const group = node("section");
      group.append(node("h3", title));
      const facts = node("dl");
      for (const [key, label] of fields) {
        const value = policy[key];
        const text = typeof value === "boolean" ? (value ? "Enabled" : "Disabled")
          : typeof value === "number" ? String(value)
            : (value || []).join(", ") || "None";
        facts.append(node("dt", label), node("dd", text));
      }
      group.append(facts);
      section.append(group);
    }
    content.replaceChildren(section);
  }

  tabs.addEventListener("click", (event) => {
    const tab = event.target.closest("button[data-tab]");
    if (tab) void select(tab.dataset.tab);
  });
  close.addEventListener("click", hide);
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !panel.hidden) {
      event.stopPropagation();
      hide();
    }
  });

  return { open, hide, select, refresh: () => select(activeTab()), isOpen: () => !panel.hidden };
}
