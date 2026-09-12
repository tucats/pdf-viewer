// pdfbrowser's frontend. No framework and no build step - this is a
// development tool (see cmd/pdfbrowser's package doc comment in
// main.go), so a single plain-JS file kept small enough to read
// top-to-bottom is worth more here than any tooling would add.
"use strict";

// --- Document state -------------------------------------------------
//
// generation and pageCount describe whatever document the server
// currently has loaded (see server.go's browserServer doc comment for
// why the server only ever holds one document at a time, and what
// "generation" means). currentPage is which page the main viewer is
// showing right now.
let generation = 0;
let pageCount = 0;
let currentPage = 0;

// thumbObserver lazily triggers each thumbnail's image request only
// once its placeholder actually scrolls into view, so dropping a
// many-hundred-page document doesn't fire off hundreds of render
// requests at once.
let thumbObserver = null;

// --- Element references ----------------------------------------------

const gutterEl = document.getElementById("gutter");
const viewerEl = document.getElementById("viewer");
const dropHintEl = document.getElementById("dropHint");
const pageImageEl = document.getElementById("pageImage");
const diagnosticsEl = document.getElementById("diagnostics");
const diagnosticsListEl = document.getElementById("diagnosticsList");
const prevBtn = document.getElementById("prevBtn");
const nextBtn = document.getElementById("nextBtn");
const pageLabelEl = document.getElementById("pageLabel");
const fontSubCheckbox = document.getElementById("fontSubCheckbox");
const statusTextEl = document.getElementById("statusText");
const quitBtn = document.getElementById("quitBtn");

// Only a page carrying the launch capability may stop pdfbrowser when it is
// closed. Pages opened directly, including those started with -no-browser,
// have no session and leave the server running.
const browserSession = new URLSearchParams(window.location.search).get("session");

// --- Status/diagnostics helpers ---------------------------------------

function setStatus(text) {
  statusTextEl.textContent = text || "";
}

// refreshDiagnostics fetches the current document's diagnostic messages
// (see server.go's handleDiagnostics) and shows/hides the diagnostics
// panel accordingly. Called after every render (page or thumbnail)
// since any of them can be what first triggers a given message.
async function refreshDiagnostics() {
  let resp;
  try {
    resp = await fetch("/api/diagnostics");
  } catch (err) {
    return; // Diagnostics are a best-effort debugging aid; ignore transient failures.
  }
  if (!resp.ok) return;
  const body = await resp.json();
  const messages = body.messages || [];

  diagnosticsListEl.innerHTML = "";
  for (const msg of messages) {
    const li = document.createElement("li");
    li.textContent = msg;
    diagnosticsListEl.appendChild(li);
  }
  diagnosticsEl.hidden = messages.length === 0;
}

// reportPageRenderError surfaces why a page image failed to load. An
// <img> element's own "error" event carries no information about the
// failure - not even the HTTP status - so pageImage.src pointing at a
// URL that fails (e.g. Page.Render returning an error the server
// reports as a 500, per server.go's writeRenderError) would otherwise
// leave the viewer looking blank/broken with no clue why. Re-fetching
// the same URL directly recovers the server's actual error text.
async function reportPageRenderError(url) {
  // Hide the <img> itself rather than leave the browser's own broken-image
  // icon on screen - the status line and diagnostics panel below are
  // where the actual error text goes.
  pageImageEl.hidden = true;

  let resp;
  try {
    resp = await fetch(url);
  } catch (err) {
    setStatus("Rendering failed: " + err);
    return;
  }
  setStatus("Rendering failed: " + (await resp.text()).trim());
  refreshDiagnostics();
}

// --- Loading a document ------------------------------------------------

async function uploadFile(file) {
  setStatus("Uploading " + file.name + "...");
  let resp;
  try {
    resp = await fetch("/api/open", { method: "POST", body: file });
  } catch (err) {
    setStatus("Upload failed: " + err);
    return;
  }
  if (!resp.ok) {
    setStatus("Upload failed: " + (await resp.text()));
    return;
  }
  const body = await resp.json();
  onDocumentLoaded(body);
  setStatus(file.name + " (" + body.pageCount + " page" + (body.pageCount === 1 ? "" : "s") + ")");
}

function onDocumentLoaded(body) {
  generation = body.generation;
  pageCount = body.pageCount;
  currentPage = 0;

  dropHintEl.hidden = true;
  pageImageEl.hidden = false;

  buildGutter();
  showPage(0);
}

// --- Thumbnail gutter ---------------------------------------------------

function buildGutter() {
  if (thumbObserver) {
    thumbObserver.disconnect();
  }
  gutterEl.innerHTML = "";

  thumbObserver = new IntersectionObserver(onThumbIntersect, {
    root: gutterEl,
    rootMargin: "200px 0px", // start loading a little before a thumbnail is actually visible
  });

  for (let i = 0; i < pageCount; i++) {
    const thumb = document.createElement("div");
    thumb.className = "thumb";
    thumb.dataset.page = String(i);
    thumb.title = "Page " + (i + 1);
    thumb.addEventListener("click", () => showPage(i));

    const label = document.createElement("span");
    label.className = "thumbLabel";
    label.textContent = String(i + 1);
    thumb.appendChild(label);

    gutterEl.appendChild(thumb);
    thumbObserver.observe(thumb);
  }
  updateActiveThumb();
}

function onThumbIntersect(entries) {
  for (const entry of entries) {
    if (!entry.isIntersecting) continue;
    const thumb = entry.target;
    thumbObserver.unobserve(thumb); // only ever need to load each thumbnail once
    loadThumbnail(thumb);
  }
}

function loadThumbnail(thumb) {
  const page = Number(thumb.dataset.page);
  const url = "/api/thumbnail?gen=" + generation + "&page=" + page;
  const img = document.createElement("img");
  img.alt = "Page " + (page + 1) + " thumbnail";
  img.src = url;
  img.addEventListener("load", refreshDiagnostics);
  img.addEventListener("error", () => {
    thumb.classList.add("renderError");
    thumb.title = "Page " + (page + 1) + " failed to render - see the main viewer for details";
    refreshDiagnostics();
  });
  thumb.insertBefore(img, thumb.firstChild);
}

function updateActiveThumb() {
  for (const thumb of gutterEl.children) {
    thumb.classList.toggle("active", Number(thumb.dataset.page) === currentPage);
  }
}

// --- Main viewer / page navigation ---------------------------------------

function showPage(page) {
  if (page < 0 || page >= pageCount) return;
  currentPage = page;
  const url = "/api/page?gen=" + generation + "&page=" + page;
  pageImageEl.src = url;
  pageImageEl.addEventListener("load", () => { pageImageEl.hidden = false; setStatus(""); refreshDiagnostics(); }, { once: true });
  pageImageEl.addEventListener("error", () => reportPageRenderError(url), { once: true });

  pageLabelEl.textContent = "Page " + (page + 1) + " of " + pageCount;
  prevBtn.disabled = page <= 0;
  nextBtn.disabled = page >= pageCount - 1;
  updateActiveThumb();

  const activeThumb = gutterEl.children[page];
  if (activeThumb) {
    activeThumb.scrollIntoView({ block: "nearest" });
  }
}

prevBtn.addEventListener("click", () => showPage(currentPage - 1));
nextBtn.addEventListener("click", () => showPage(currentPage + 1));

// --- Drag and drop ---------------------------------------------------------

// The whole app (not just the viewer pane) accepts a drop, and every
// dragover/drop is prevented at the window level too - without that,
// missing the drop zone by a few pixels makes the browser navigate away
// to display the raw PDF instead of handing it to pdfbrowser.
for (const evt of ["dragenter", "dragover", "dragleave", "drop"]) {
  window.addEventListener(evt, (e) => e.preventDefault());
}

viewerEl.addEventListener("dragenter", () => viewerEl.classList.add("dragover"));
viewerEl.addEventListener("dragleave", (e) => {
  if (e.target === viewerEl) viewerEl.classList.remove("dragover");
});
viewerEl.addEventListener("drop", (e) => {
  viewerEl.classList.remove("dragover");
  const file = e.dataTransfer.files && e.dataTransfer.files[0];
  if (file) uploadFile(file);
});

// --- Font substitution toggle ------------------------------------------

// initConfig fetches the server's initial configuration (currently just
// -use-system-fonts - see server.go's handleConfig) and sets the
// checkbox to match, without going through the "change" handler below -
// this is reporting state the server already has, not a change the
// server needs to be told about (it already knows, since that flag is
// where the value came from in the first place).
async function initConfig() {
  let resp;
  try {
    resp = await fetch("/api/config");
  } catch (err) {
    return; // Best-effort; the checkbox just starts unchecked.
  }
  if (!resp.ok) return;
  const body = await resp.json();
  fontSubCheckbox.checked = !!body.useSystemFonts;
}
initConfig();

fontSubCheckbox.addEventListener("change", async () => {
  const enabled = fontSubCheckbox.checked;
  setStatus(enabled ? "Enabling system font substitution..." : "Disabling system font substitution...");

  let resp;
  try {
    resp = await fetch("/api/font-substitution", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled }),
    });
  } catch (err) {
    setStatus("Request failed: " + err);
    return;
  }
  if (!resp.ok) {
    setStatus("Request failed: " + (await resp.text()));
    return;
  }

  const body = await resp.json();
  setStatus("");
  if (!body.hasDocument) return; // Preference recorded; nothing loaded yet to re-render.

  // Font substitution is decided when a document is opened (see
  // pdfviewer.WithFontSubstitution), so the server had to re-open the
  // document to apply the change - which bumped its generation number
  // (see server.go's handleFontSubstitution). Every existing thumbnail/
  // page image URL embeds the old generation, so all of them need to be
  // re-requested against the new one.
  onDocumentLoaded(body);
});

// --- Quit ------------------------------------------------------------------

quitBtn.addEventListener("click", async () => {
  quitBtn.disabled = true;
  try {
    await fetch("/api/quit", { method: "POST" });
  } catch (err) {
    // The server closing its listener as part of shutting down can
    // itself look like a fetch failure - that is the expected outcome
    // of asking it to quit, not an error to report.
  }
  setStatus("pdfbrowser has stopped. You may close this tab.");
  prevBtn.disabled = true;
  nextBtn.disabled = true;
  fontSubCheckbox.disabled = true;
});

// pagehide fires both when this tab is actually closing and when it is
// merely reloading (e.g. after editing this very file) or navigating
// away - there is no client-side way to tell those apart. So this always
// sends the beacon, and it is server.go's quitGraceDelay that tells them
// apart in practice: a reload's next request (for "/", app.js, ...)
// arrives well within that delay and cancels the pending quit, while an
// actual close sends nothing further and lets it fire.
window.addEventListener("pagehide", () => {
  if (!browserSession) return;
  navigator.sendBeacon("/api/browser-closed?session=" + encodeURIComponent(browserSession));
});
