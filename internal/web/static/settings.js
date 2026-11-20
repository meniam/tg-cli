export function bindSettings({ api, node, button, action, notify, panelContent, state, loadChats, renderFolders, renderChats }) {
  function field(label, control) {
    const wrapped = node("label", label);
    wrapped.append(control);
    return wrapped;
  }

  function submitButton(label) {
    const item = node("button", label);
    item.type = "submit";
    return item;
  }

  async function renderAccessSettings() {
    const policy = await api("access.view");
    if (policy.closed) panelContent.replaceChildren(node("p", "The access policy is unavailable. All chats are closed."));
    else panelContent.replaceChildren();
    const groups = [
      ["Chat rules", [["allow", "Allow list"], ["block", "Block list"], ["write", "Message writes"], ["admin", "Chat administration"]]],
      ["Account permissions", [["dialogs", "Manage dialogs and folders"], ["sessions", "Terminate sessions"], ["create", "Create chats"], ["profile", "Edit profile"]]],
      ["Directories", [["files", "Send files from"], ["downloads", "Download files to"]]],
    ];
    for (const [title, fields] of groups) {
      const section = node("section");
      section.append(node("h2", title));
      const entries = node("dl");
      for (const [key, label] of fields) {
        const value = policy[key];
        const text = typeof value === "boolean" ? (value ? "Enabled" : "Disabled")
          : typeof value === "number" ? String(value) : (value || []).join(", ") || "None";
        entries.append(node("dt", label), node("dd", text));
      }
      section.append(entries);
      panelContent.append(section);
    }
  }

  async function showChatSettings() {
    const chat = state.chat;
    const settings = await api("chat.settings", { chat });
    if (chat !== state.chat) return;
    const current = state.chats.find((item) => String(item.chat_id) === chat);
    const form = node("form");
    const mode = node("select");
    mode.append(new Option("Automatic", "auto"), new Option("Skip this chat", "skip"), new Option("Prioritize this chat", "first"));
    mode.value = settings.history_mode;
    form.append(field("Background history", mode), submitButton("Save"));
    form.addEventListener("submit", action(async (event) => {
      event.preventDefault();
      await api("chat.settings.set", { chat, history_mode: mode.value }, true);
      notify("History setting saved.");
    }));
    panelContent.append(node("h2", "Local settings"), form);
    const dialog = node("section");
    const controls = [
      ["mute", "unmute", current?.muted, "Mute", "Unmute"],
      ["archive", "unarchive", current?.archived, "Archive", "Unarchive"],
      ["pin_dialog", "unpin_dialog", current?.pinned, "Pin chat", "Unpin chat"],
    ];
    for (const [on, off, enabled, onLabel, offLabel] of controls) {
      const control = button(enabled ? offLabel : onLabel, action(async () => {
        const params = on === "mute" ? { chat } : { chats: [chat] };
        await api("chat." + (enabled ? off : on), params, true);
        await loadChats();
        notify("Telegram setting updated.");
        document.getElementById("details").click();
      }));
      dialog.append(control);
    }
    const folders = state.folders.filter((folder) => folder.rules);
    if (folders.length) {
      const select = node("select");
      select.append(...folders.map((folder) => new Option(folder.title, folder.id)));
      const update = (add) => action(async () => {
        const id = Number(select.value);
        await api("folder.edit", { id, [add ? "add" : "remove"]: [chat] }, true);
        state.folders = (await api("folder.list")).folders || [];
        renderFolders();
        renderChats();
        notify("Folder updated.");
      });
      dialog.append(field("Folder", select), button("Add to folder", update(true)), button("Remove from folder", update(false)));
    }
    panelContent.append(node("h2", "Telegram settings"), dialog);

  }

  return { renderAccessSettings, showChatSettings };
}
