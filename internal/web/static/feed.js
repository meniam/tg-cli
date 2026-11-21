// The reading column: grouping by local day and author, message cards,
// formatting from Telegram entities, lazy thumbnails and the reading anchor.

import { node, button, dayFormat, timeFormat, clock, safeLink } from "/api.js";

const thumbnailTypes = ["photo", "video", "sticker", "document", "animation", "video_note"];
const GROUP_WINDOW_SECONDS = 15 * 60;

/**
 * Turns a sorted page of messages into what the feed draws: a day separator
 * before the first message of a local day, and a tighter card when the previous
 * message has the same author, direction and day.
 */
export function groupMessages(messages) {
  const groups = [];
  let lastDay = "";
  let previous = null;
  for (const message of messages) {
    const date = new Date(message.date);
    const day = dayFormat.format(date);
    if (day !== lastDay) {
      groups.push({ kind: "day", label: day });
      lastDay = day;
      previous = null;
    }
    const grouped =
      previous !== null &&
      Boolean(previous.out) === Boolean(message.out) &&
      (previous.sender?.id || 0) === (message.sender?.id || 0) &&
      Math.abs(new Date(previous.date) - date) <= GROUP_WINDOW_SECONDS * 1000 &&
      !message.service &&
      !previous.service;
    groups.push({ kind: "message", message, grouped });
    previous = message;
  }
  // The last message of a run carries the tail; a service event never does.
  for (let index = 0; index < groups.length; index++) {
    const group = groups[index];
    if (group.kind !== "message" || group.message.service) continue;
    const next = groups[index + 1];
    group.tail = !next || next.kind === "day" || next.message?.service || !next.grouped;
  }
  return groups;
}

const svgNS = "http://www.w3.org/2000/svg";

/** A small inline glyph built from path data, so no icon font is needed. */
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

/**
 * The reply strip: a short excerpt of the quoted message when it is loaded,
 * otherwise the plain "Reply" label. Both open the original on click.
 */
/**
 * The reply strip text: a short excerpt of the quoted message when it is in the
 * rows being drawn, otherwise the plain "Reply" label. Both navigate.
 */
function quotedExcerpt(rows, id) {
  const quoted = rows.find((row) => row.id === id);
  if (!quoted) return "Reply";
  const text = quoted.text?.replace(/\s+/g, " ").trim();
  if (text) return text.length > 90 ? text.slice(0, 90) + "…" : text;
  if (quoted.media_type) return mediaLabel(quoted);
  return "Reply";
}

const tagFor = {
  bold: "strong",
  italic: "em",
  code: "code",
  pre: "pre",
  blockquote: "blockquote",
  underline: "u",
  strike: "s",
};

/**
 * Renders text with Telegram entities as DOM nodes. Offsets are UTF-16 code
 * units, which is what slice() uses; an overlapping or out-of-range entity is
 * skipped rather than drawn wrong, and text is always inserted as text.
 */
export function formatText(text, entities = []) {
  const fragment = document.createDocumentFragment();
  let offset = 0;
  const sorted = [...entities].sort((a, b) => a.offset - b.offset || b.length - a.length);
  for (const entity of sorted) {
    const end = entity.offset + entity.length;
    if (entity.offset < offset || end > text.length || end <= entity.offset) continue;
    const piece = text.slice(entity.offset, end);
    const formatted = entity.type === "url" || entity.type === "text_url"
      ? safeLink(entity.url || piece, piece)
      : tagFor[entity.type] ? node(tagFor[entity.type], piece) : null;
    if (!formatted) continue;
    fragment.append(text.slice(offset, entity.offset), formatted);
    offset = end;
  }
  fragment.append(text.slice(offset));
  return fragment;
}

// A webpage is a link preview riding on the text, not an attachment of its own,
// so it never becomes a media row.
const silentMedia = new Set(["webpage", "other", ""]);

const mediaNames = {
  photo: "Photo",
  video: "Video",
  animation: "GIF",
  gif: "GIF",
  document: "File",
  voice: "Voice message",
  video_note: "Video message",
  sticker: "Sticker",
  audio: "Audio",
  poll: "Poll",
  location: "Location",
  contact: "Contact",
  dice: "Dice",
  game: "Game",
  invoice: "Invoice",
};

/** The readable name of an attachment, never an internal type. */
function mediaLabel(message) {
  const name = message.media_name || mediaNames[message.media_type] || "Attachment";
  if (!message.media_size) return name;
  return name + " · " + Math.max(1, Math.round(message.media_size / 1024)) + " KB";
}

/**
 * Service events as short plain chips. The daemon sends the Telegram action
 * name (pin_message, chat_create, …); an unknown one becomes a neutral chip
 * rather than a raw identifier.
 */
const serviceNames = {
  pin_message: "Pinned a message",
  chat_create: "Group created",
  channel_create: "Channel created",
  chat_edit_title: "Title changed",
  chat_edit_photo: "Photo changed",
  chat_delete_photo: "Photo removed",
  chat_add_user: "Members added",
  chat_delete_user: "Member left",
  chat_joined_by_link: "Joined by link",
  chat_joined_by_request: "Joined by request",
  channel_migrate: "Chat upgraded",
  chat_edit_about: "Description changed",
  history_clear: "History cleared",
  custom_action: "Service event",
};

function serviceText(kind) {
  return serviceNames[kind] || "Service event";
}

/**
 * Draws one message card. Nothing here fetches: the feed's observer attaches
 * thumbnails later, so a redraw of one card never restarts image requests.
 */
export function messageCard(message, { anchor = "", chat = "", onJump = () => {}, grouped = false, rows = [] } = {}) {
  const card = node("article", "", "message" + (message.out ? " out" : ""));
  card.id = "msg-" + message.id;
  card.dataset.id = String(message.id);
  card.tabIndex = -1;
  if (String(message.id) === anchor) card.classList.add("highlight");
  card.setAttribute("aria-label", [message.sender?.name, message.text, message.media_type]
    .filter(Boolean).join(", ") || "Message " + message.id);

  // A service event is its own centred neutral chip: it never takes the
  // outgoing shell, an author line, a quote strip or an attachment.
  if (message.service) {
    card.classList.remove("out");
    card.classList.add("service");
    const chip = node("p", serviceText(message.service), "service-chip");
    // A service event that answers something keeps the way to it.
    if (message.reply_to) {
      const card2 = button("", () => onJump(message.reply_to), "service-chip service-action");
      card2.append(node("span", serviceText(message.service)));
      card2.setAttribute("aria-label", `${serviceText(message.service)}. Show the replied message`);
      card.append(card2);
    } else {
      card.append(chip);
    }
    const serviceTime = node("time", timeFormat.format(new Date(message.date)));
    serviceTime.dateTime = message.date;
    serviceTime.title = clock(message.date);
    card.append(serviceTime);
    return card;
  }

  // Own messages carry no author, and a grouped message repeats no name: the
  // time and the side already say who wrote it.
  if (message.sender?.name && !grouped && !message.out) {
    const meta = node("header", "", "message-meta");
    meta.append(node("strong", message.sender.name, "sender"));
    card.append(meta);
  }
  if (message.forwarded) card.append(node("p", "Forwarded message", "forwarded"));
  if (message.reply_to) {
    // The excerpt when the quoted message is loaded, otherwise a plain label;
    // both navigate to the original.
    const quoted = quotedExcerpt(rows, message.reply_to);
    card.append(button(quoted, () => onJump(message.reply_to), "quote"));
  }
  if (message.text) {
    const body = node("p", "", "message-text");
    body.append(formatText(message.text, message.entities || []));
    card.append(body);
  }
  if (message.media_type && !silentMedia.has(message.media_type)) {
    const media = node("div", "", "media");
    const caption = node("span", mediaLabel(message), "media-label");
    if (thumbnailTypes.includes(message.media_type) && chat) {
      // The frame reserves the space, so the picture never shifts the feed. The
      // image stays visible-but-empty instead of hidden: a hidden lazy image is
      // never loaded, and it would stay hidden for good.
      const frame = node("span", "", "media-frame");
      const image = node("img");
      image.alt = message.media_type.replace("_", " ") + " preview";
      image.decoding = "async";
      image.addEventListener("load", () => frame.classList.add("loaded"));
      image.addEventListener("error", () => frame.classList.add("failed"));
      frame.append(image);
      media.dataset.thumbnail = "/api/thumb?chat=" + encodeURIComponent(chat) + "&id=" + message.id;
      media.append(frame);
    }
    media.append(caption);
    card.append(media);
  }
  if (message.reactions?.length) {
    card.append(node("p", message.reactions.map((item) => `${item.emoji} ${item.count}`).join("  "), "reactions"));
  }
  const footer = node("div", "", "message-footer");
  const time = node("time", timeFormat.format(new Date(message.date)) + (message.edited ? " · edited" : ""));
  time.dateTime = message.date;
  time.title = clock(message.date);
  footer.append(time);
  // The Telegram link is a compact icon next to the time, not a row of its own;
  // the whitelist in safeLink still decides whether it exists at all.
  if (message.link) {
    const link = safeLink(message.link, "");
    if (link) {
      link.className = "message-link";
      link.setAttribute("aria-label", "Open this message in Telegram");
      link.title = "Open in Telegram";
      link.append(glyph("message-link-icon", [
        ["path", { d: "M10.5 3.5 16 3.5 16 9", fill: "none", stroke: "currentColor", "stroke-width": "1.7", "stroke-linecap": "round", "stroke-linejoin": "round" }],
        ["path", { d: "M16 3.5 8.5 11", fill: "none", stroke: "currentColor", "stroke-width": "1.7", "stroke-linecap": "round" }],
        ["path", { d: "M13.5 11.5v3a1.5 1.5 0 0 1-1.5 1.5H4.5A1.5 1.5 0 0 1 3 14.5V7a1.5 1.5 0 0 1 1.5-1.5h3", fill: "none", stroke: "currentColor", "stroke-width": "1.7", "stroke-linecap": "round", "stroke-linejoin": "round" }],
      ]));
      footer.append(link);
    }
  }
  card.append(footer);
  return card;
}

function firstDayLabel(container) {
  const day = container.querySelector(".day");
  return day ? day.textContent : "";
}

function daysIn(fragment) {
  return [...fragment.querySelectorAll(".day")];
}

/**
 * The feed owns scrolling and image loading. It keeps the first visible message
 * and its offset across a prepend, reports whether the reader is at the bottom,
 * and never jumps on its own.
 */
export function createFeed({ onOlder, onJump }) {
  const timeline = document.getElementById("timeline");
  const messages = document.getElementById("messages");
  const olderRow = document.getElementById("older-row");
  const older = document.getElementById("older");
  const jump = document.getElementById("jump-new");
  const observer = new IntersectionObserver(onIntersect, { root: timeline });
  // The messages currently drawn, kept in step with every draw, replace and
  // removal so a reply strip never quotes a row that is no longer shown.
  let feedRows = [];
  const waiting = new Set();
  const thumbnails = new Map();
  let downloading = 0;
  let pausedUntil = 0;
  let pauseTimer;
  // One guard for both triggers: the button and the near-top scroll share it,
  // so a scroll burst cannot start a second page while one is in flight.
  let loadingOlder = false;
  // Automatic loading needs an explicit upward intent: a wheel turn, a finger
  // drag or an up-key. Intent is bounded, and the scroll event consumes it only
  // when the reader is really near the top. A programmatic scrollTop change, a
  // resize or a direct link that mounts near the top carries no intent, so it
  // never fetches older history.
  let intentUntil = 0;
  const INTENT_MS = 700;

  function onIntersect(entries) {
    for (const entry of entries) {
      if (entry.isIntersecting) {
        waiting.add(entry.target);
        showEmbedded(entry.target);
      } else {
        waiting.delete(entry.target);
      }
    }
    void download();
  }

  /** Shows the preview embedded in the message while the full one is pending. */
  function showEmbedded(media) {
    const image = media.querySelector("img");
    if (!image) return;
    const cached = thumbnails.get(media.dataset.thumbnail);
    image.src = cached || media.dataset.thumbnail + "&preview=1";
  }

  /** At most three downloads at once, a Retry-After pause, a small blob cache. */
  async function download() {
    if (Date.now() < pausedUntil) {
      clearTimeout(pauseTimer);
      pauseTimer = setTimeout(download, pausedUntil - Date.now());
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
            const pause = Number(response.headers.get("Retry-After")) || 30;
            pausedUntil = Math.max(pausedUntil, Date.now() + 1000 * pause);
            if (media.isConnected) waiting.add(media);
          } else if (response.ok && media.isConnected) {
            remember(url, URL.createObjectURL(await response.blob()));
            const image = media.querySelector("img");
            if (image) image.src = thumbnails.get(url);
          } else {
            await response.body?.cancel();
          }
        } catch {
          // The card keeps its media label and the embedded preview.
        } finally {
          downloading--;
          void download();
        }
      })();
    }
  }

  function remember(url, objectURL) {
    if (thumbnails.size >= 250) {
      const oldest = thumbnails.keys().next().value;
      URL.revokeObjectURL(thumbnails.get(oldest));
      thumbnails.delete(oldest);
    }
    thumbnails.set(url, objectURL);
  }

  /**
   * Puts messages in the DOM in one of three modes. "reset" replaces the feed,
   * "prepend" inserts an older page above it and keeps the reading anchor, and
   * "append" adds newer messages below without moving the reader.
   */
  function draw(list, { mode = "reset", anchor = "", chat = "" } = {}) {
    if (mode === "reset") feedRows = list.slice();
    else if (mode === "prepend") feedRows = [...list, ...feedRows];
    else feedRows = [...feedRows, ...list];
    const keep = mode === "reset" ? "" : firstVisibleID();
    const offset = keep ? offsetOf(keep) : 0;
    const atEnd = mode === "append" ? atBottom() : false;
    const fragment = document.createDocumentFragment();
    for (const group of groupMessages(list)) {
      if (group.kind === "day") {
        fragment.append(node("div", group.label, "day"));
        continue;
      }
      // A page may overlap what is already drawn: drop the card, not the page.
      if (mode !== "reset" && document.getElementById("msg-" + group.message.id)) continue;
      const element = messageCard(group.message, { anchor, chat, onJump, grouped: group.grouped, rows: feedRows });
      if (group.grouped) element.classList.add("grouped");
      if (group.tail) element.classList.add("tail");
      fragment.append(element);
    }
    if (mode === "prepend") {
      dropEmptyEdgeDays(fragment);
      // One day must have one separator: when the page ends inside the day the
      // feed already starts with, the older separator wins and the newer goes.
      const days = daysIn(fragment);
      const newest = days[days.length - 1];
      if (newest && newest.textContent === firstDayLabel(messages)) {
        messages.querySelector(".day")?.remove();
      }
      messages.prepend(fragment);
      observeAll(messages);
      restore(keep, offset);
      return;
    }
    if (mode === "append") {
      dropEmptyEdgeDays(fragment);
      const days = daysIn(fragment);
      if (days[0] && days[0].textContent === lastDayLabel(messages)) days[0].remove();
      messages.append(fragment);
      observeAll(messages);
      if (atEnd) toBottom();
      return;
    }
    observer.disconnect();
    const empty = node("p", "No messages in the local history of this chat.", "empty");
    messages.replaceChildren(fragment.childNodes.length ? fragment : empty);
    observeAll(messages);
  }

  /** A separator with no message under it would just repeat the next one. */
  function dropEmptyEdgeDays(fragment) {
    while (fragment.firstChild?.classList?.contains("day")) fragment.firstChild.remove();
    while (fragment.lastChild?.classList?.contains("day")) fragment.lastChild.remove();
  }

  function lastDayLabel(container) {
    const days = container.querySelectorAll(".day");
    return days.length ? days[days.length - 1].textContent : "";
  }

  function observeAll(root) {
    for (const media of root.querySelectorAll("[data-thumbnail]")) observer.observe(media);
  }

  /** Redraws one message in place, keeping its neighbours and loaded images. */
  function replaceCard(message, options) {
    const existing = document.getElementById("msg-" + message.id);
    // Keep the grouping the card already has.
    const grouped = existing?.classList.contains("grouped") || false;
    const index = feedRows.findIndex((row) => row.id === message.id);
    if (index >= 0) feedRows[index] = message;
    else feedRows = [...feedRows, message];
    const card = messageCard(message, { ...options, grouped, rows: feedRows });
    if (existing) existing.replaceWith(card);
    else messages.append(card);
    observeAll(card);
    return card;
  }

  function removeCards(ids) {
    feedRows = feedRows.filter((row) => !ids.includes(row.id));
    for (const id of ids) document.getElementById("msg-" + id)?.remove();
  }

  function firstVisibleID() {
    const top = timeline.getBoundingClientRect().top;
    for (const card of messages.children) {
      if (card.classList.contains("message") && card.getBoundingClientRect().bottom > top) {
        return card.dataset.id;
      }
    }
    return "";
  }

  function offsetOf(id) {
    const card = document.getElementById("msg-" + id);
    return card ? card.getBoundingClientRect().top - timeline.getBoundingClientRect().top : 0;
  }

  function restore(id, offset) {
    if (!id) return;
    const card = document.getElementById("msg-" + id);
    if (!card) return;
    timeline.scrollTop += card.getBoundingClientRect().top - timeline.getBoundingClientRect().top - offset;
  }

  function scrollToMessage(id, behavior = "auto") {
    const card = document.getElementById("msg-" + id);
    if (!card) return false;
    card.scrollIntoView({ block: "center", behavior });
    card.focus({ preventScroll: true });
    return true;
  }

  function atBottom() {
    return timeline.scrollHeight - timeline.scrollTop - timeline.clientHeight < 80;
  }

  function toBottom() {
    timeline.scrollTop = timeline.scrollHeight;
  }

  function showJump(count) {
    jump.hidden = count === 0;
    jump.textContent = count === 1 ? "1 new message" : count + " new messages";
  }

  /** Loads one older page; both the button and the near-top scroll use this. */
  async function loadOlder() {
    if (loadingOlder || olderRow.hidden) return;
    loadingOlder = true;
    intentUntil = 0;
    older.disabled = true;
    older.setAttribute("aria-busy", "true");
    try {
      await onOlder();
    } catch {
      // The caller reports the failure; the button stays usable.
    } finally {
      loadingOlder = false;
      older.disabled = false;
      older.removeAttribute("aria-busy");
    }
  }

  /** 'An upward gesture is happening now'; the next scroll near the top uses it. */
  function intend() {
    intentUntil = Date.now() + INTENT_MS;
    // At the very top a wheel turn changes nothing, so no scroll event follows:
    // act on the gesture itself, while the intent still holds.
    if (nearTop()) void loadOlder();
  }

  /** Near the top means "the reader asked for older messages", not "the page is short". */
  function nearTop() {
    const top = timeline.getBoundingClientRect().top;
    const first = messages.querySelector(".message");
    if (!first) return false;
    return first.getBoundingClientRect().top - top > -timeline.clientHeight * 0.5;
  }

  older.addEventListener("click", () => { void loadOlder(); });
  jump.addEventListener("click", () => {
    showJump(0);
    toBottom();
  });

  timeline.addEventListener("wheel", (event) => {
    if (event.deltaY < 0) intend();
  }, { passive: true });
  timeline.addEventListener("touchmove", intend, { passive: true });
  timeline.addEventListener("keydown", (event) => {
    if (["PageUp", "ArrowUp", "Home"].includes(event.key)) intend();
  });
  // The scroll consumes a live intent only near the top. Without an intent it
  // does nothing, so programmatic scrolls and layout changes never fetch.
  timeline.addEventListener("scroll", () => {
    if (atBottom() && !jump.hidden) showJump(0);
    // loadOlder clears the intent before it awaits, and its own redraw happens
    // while it holds the in-flight guard, so a feed-owned scroll cannot feed back.
    if (Date.now() > intentUntil || !nearTop()) return;
    void loadOlder();
  }, { passive: true });
  timeline.addEventListener("keydown", (event) => {
    if (event.key !== "PageUp" && event.key !== "PageDown") return;
    event.preventDefault();
    const step = (event.key === "PageUp" ? -1 : 1) * timeline.clientHeight * 0.9;
    timeline.scrollTop += step;
  });

  return {
    draw,
    replaceCard,
    removeCards,
    scrollToMessage,
    atBottom,
    toBottom,
    showJump,
    firstVisibleID,
    offsetOf,
    restore,
    setOlderVisible(visible) {
      olderRow.hidden = !visible;
    },
    scrollTop: () => timeline.scrollTop,
    scrollTo(value) {
      timeline.scrollTop = value;
    },
    /** Drops the whole feed, the queued downloads and the jump counter. */
    clear() {
      observer.disconnect();
      waiting.clear();
      messages.replaceChildren();
      showJump(0);
      intentUntil = 0;
      // Drop the previous chat's rows: a reply strip must not quote them.
      feedRows = [];
    },
  };
}
