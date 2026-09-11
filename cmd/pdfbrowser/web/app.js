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
  const img = document.createElement("img");
  img.alt = "Page " + (page + 1) + " thumbnail";
  img.src = "/api/thumbnail?gen=" + generation + "&page=" + page;
  img.addEventListener("load", refreshDiagnostics);
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
  pageImageEl.src = "/api/page?gen=" + generation + "&page=" + page;
  pageImageEl.addEventListener("load", refreshDiagnostics, { once: true });

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
