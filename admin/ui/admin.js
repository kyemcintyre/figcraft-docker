"use strict";

// FigCraft admin page. Every change sends X-Figcraft-Admin: 1 (the server refuses changes
// without it); the session is an HttpOnly cookie the page never sees.

const $ = (id) => document.getElementById(id);
let currentFile = null; // { id, name }

function status(message, kind) {
  const el = $("status");
  el.textContent = message || "";
  el.className = kind || "";
}

async function api(method, path, body) {
  const opts = { method, headers: { "X-Figcraft-Admin": "1" }, credentials: "same-origin" };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const res = await fetch("/admin/api/" + path, opts);
  let data = {};
  try { data = await res.json(); } catch (_) { /* empty or not JSON */ }
  if (res.status === 401 && path !== "login" && path !== "password") {
    show("login");
    throw new Error("Your session ended: sign in again.");
  }
  if (!res.ok) throw new Error(data.error || "Request failed (" + res.status + ")");
  return data;
}

function show(view) {
  const signedIn = view !== "login";
  $("nav").hidden = !signedIn;
  $("login").hidden = view !== "login";
  $("password").hidden = view !== "password";
  $("files").hidden = !signedIn || view === "password";
  $("links").hidden = !signedIn || view === "password" || !currentFile;
  if (view === "login") $("login-password").focus();
}

function cell(text, cls) {
  const td = document.createElement("td");
  td.textContent = text;
  if (cls) td.className = cls;
  return td;
}

function button(text, cls, onClick) {
  const b = document.createElement("button");
  b.type = "button";
  b.textContent = text;
  if (cls) b.className = cls;
  b.addEventListener("click", onClick);
  return b;
}

async function loadFiles() {
  const { files } = await api("GET", "files");
  const body = $("files-body");
  body.replaceChildren();
  for (const f of files) {
    const tr = document.createElement("tr");
    tr.append(
      cell(f.name),
      cell(f.id, "mono hide-narrow"),
      cell(String(f.people), "num"),
      cell(f.size, "hide-narrow"),
      cell(f.created, "hide-narrow"),
      cell(f.idle),
    );
    const actions = cell("", "actions");
    actions.append(
      button("Links", "secondary", () => openLinks(f)),
      button("Remove", "danger", () => removeFile(f)),
    );
    tr.append(actions);
    body.append(tr);
  }
  $("files-empty").hidden = files.length > 0;
}

async function openLinks(f) {
  currentFile = { id: f.id, name: f.name };
  $("links-file").textContent = f.name;
  $("created").hidden = true;
  show("files");
  try {
    await loadLinks();
    $("links").scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (e) {
    status(e.message, "error");
  }
}

async function loadLinks() {
  const { links } = await api("GET", "files/" + encodeURIComponent(currentFile.id) + "/links");
  const body = $("links-body");
  body.replaceChildren();
  for (const l of links) {
    const tr = document.createElement("tr");
    tr.append(cell(l.id, "mono"), cell(l.access), cell(l.created, "hide-narrow"), cell(l.label));
    const actions = cell("", "actions");
    actions.append(button("Revoke", "danger", () => revokeLink(l)));
    tr.append(actions);
    body.append(tr);
  }
  $("links-empty").hidden = links.length > 0;
}

async function revokeLink(l) {
  const what = l.label ? '"' + l.label + '"' : l.id;
  if (!confirm("Revoke the " + l.access + " link " + what + "?\n\nIt stops working at once and anyone using it is disconnected.")) return;
  try {
    await api("DELETE", "files/" + encodeURIComponent(currentFile.id) + "/links/" + encodeURIComponent(l.id));
    status("Revoked " + what + ".", "ok");
    await loadLinks();
  } catch (e) {
    status(e.message, "error");
  }
}

async function removeFile(f) {
  const typed = prompt(
    'Remove "' + f.name + '" from the server?\n\nThe file and all its links are deleted for everyone and can\'t be brought back. ' +
    "Keep a copy first (open it from your edit link, then Leave session and save).\n\n" +
    "Type the file name (or its ID, " + f.id + ") to confirm:",
  );
  if (typed === null) return;
  if (typed.trim() !== f.name && typed.trim() !== f.id) {
    status("That didn't match the name or ID: nothing was removed.", "error");
    return;
  }
  try {
    await api("DELETE", "files/" + encodeURIComponent(f.id));
    if (currentFile && currentFile.id === f.id) {
      currentFile = null;
      $("links").hidden = true;
    }
    status('Removed "' + f.name + '".', "ok");
    await loadFiles();
  } catch (e) {
    status(e.message, "error");
  }
}

async function start() {
  try {
    const { signedIn } = await api("GET", "session");
    if (!signedIn) return show("login");
    show("files");
    await loadFiles();
  } catch (e) {
    status(e.message, "error");
  }
}

document.addEventListener("DOMContentLoaded", () => {
  $("login-form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    try {
      await api("POST", "login", { password: $("login-password").value });
      $("login-password").value = "";
      status("");
      show("files");
      await loadFiles();
    } catch (e) {
      status(e.message, "error");
    }
  });

  $("logout").addEventListener("click", async () => {
    try { await api("POST", "logout"); } catch (_) { /* signed out anyway */ }
    currentFile = null;
    status("Signed out.", "ok");
    show("login");
  });

  $("show-password").addEventListener("click", () => {
    status("");
    show("password");
    $("pw-current").focus();
  });
  $("pw-cancel").addEventListener("click", () => {
    $("password-form").reset();
    show("files");
  });
  $("password-form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    if ($("pw-new").value !== $("pw-confirm").value) {
      status("The new passwords don't match.", "error");
      return;
    }
    try {
      await api("POST", "password", { current: $("pw-current").value, new: $("pw-new").value });
      $("password-form").reset();
      status("Password changed. Other sessions were signed out.", "ok");
      show("files");
    } catch (e) {
      status(e.message, "error");
    }
  });

  $("refresh").addEventListener("click", async () => {
    try {
      await loadFiles();
      if (currentFile) await loadLinks();
      status("Up to date.", "ok");
    } catch (e) {
      status(e.message, "error");
    }
  });

  $("links-close").addEventListener("click", () => {
    currentFile = null;
    $("links").hidden = true;
  });

  $("create-form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    try {
      const res = await api("POST", "files/" + encodeURIComponent(currentFile.id) + "/links", {
        access: $("create-access").value,
        label: $("create-label").value.trim(),
      });
      $("created-browser").value = res.browser || "(set FIGCRAFT_WEB_URL to get a browser link)";
      $("created-link").value = res.link.link;
      $("created").hidden = false;
      $("create-label").value = "";
      status("Made a " + res.link.access + " link.", "ok");
      await loadLinks();
    } catch (e) {
      status(e.message, "error");
    }
  });

  document.querySelectorAll("[data-copy]").forEach((b) => {
    b.addEventListener("click", async () => {
      const input = $(b.dataset.copy);
      try {
        await navigator.clipboard.writeText(input.value);
        status("Copied.", "ok");
      } catch (_) {
        input.select();
        status("Select and copy it by hand.", "error");
      }
    });
  });

  start();
});
