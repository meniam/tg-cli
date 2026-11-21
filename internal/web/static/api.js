// Shared browser plumbing for the viewer: the read-only API call, DOM helpers,
// formatters and the status line. Nothing here writes to the daemon.

const status = () => document.getElementById("status");

export function notify(message) {
  const output = status();
  output.textContent = message;
  output.hidden = !message;
}

export function node(tag, text = "", className = "") {
  const element = document.createElement(tag);
  element.textContent = text;
  if (className) element.className = className;
  return element;
}

export function button(text, handler, className = "") {
  const element = node("button", text, className);
  element.type = "button";
  element.addEventListener("click", handler);
  return element;
}

/** Runs an async handler and turns a rejection into the status line. */
export function action(fn) {
  return async (...args) => {
    try {
      notify("");
      return await fn(...args);
    } catch (error) {
      notify(error.message);
      return undefined;
    }
  };
}

export class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

/**
 * POST one allowlisted read method. The gateway rejects everything else, so a
 * caller never has to think about write access.
 */
export async function api(method, params = {}) {
  const response = await fetch("/api/" + method, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(params),
  });
  if (!response.ok) {
    if (response.status === 401) {
      throw new ApiError("Session expired. Open the URL printed by tg web.", 401);
    }
    const body = (await response.text()).trim();
    throw new ApiError(body || response.statusText, response.status);
  }
  return response.json();
}

export const dayFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });
export const timeFormat = new Intl.DateTimeFormat(undefined, { timeStyle: "short" });
export const weekdayFormat = new Intl.DateTimeFormat(undefined, { weekday: "short" });

export function stamp(seconds) {
  return seconds ? new Date(seconds * 1000) : null;
}

export function shortDate(seconds) {
  if (!seconds) return "";
  const date = stamp(seconds);
  const now = new Date();
  const sameDay = date.toDateString() === now.toDateString();
  if (sameDay) return timeFormat.format(date);
  return dayFormat.format(date);
}

export function clock(iso) {
  const date = new Date(iso);
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

/**
 * A stable peer colour, as Web K picks one per id: seven named gradients
 * (_avatar.scss data-color red, orange, violet, green, cyan, pink, blue).
 */
const peerColours = ["red", "orange", "violet", "green", "cyan", "blue", "pink"];

export function avatarTint(id) {
  let hash = 0;
  const text = String(id);
  for (let index = 0; index < text.length; index++) {
    hash = (hash * 31 + text.charCodeAt(index)) & 0xffffffff;
  }
  return peerColours[Math.abs(hash) % peerColours.length];
}

/** Applies the Web K avatar gradient to an element and returns it. */
export function applyTint(element, id) {
  element.dataset.tint = avatarTint(id);
  return element;
}

export function initials(title) {
  const first = Array.from(String(title || "?").trim())[0];
  return first ? first.toLocaleUpperCase() : "?";
}

const linkProtocols = ["http:", "https:", "tg:", "mailto:"];

/** Builds an anchor for a value from the local database or Telegram text only. */
export function safeLink(value, text) {
  try {
    const url = new URL(value);
    if (!linkProtocols.includes(url.protocol)) return null;
    const link = node("a", text);
    link.href = url.href;
    link.rel = "noopener noreferrer";
    link.target = "_blank";
    return link;
  } catch {
    return null;
  }
}
