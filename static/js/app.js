// app.js — progressive enhancement only. Core flows work without JS.

// Theme toggle (dark mode persisted)
document.addEventListener("DOMContentLoaded", function () {
  var btn = document.getElementById("theme-toggle");
  if (btn) {
    btn.addEventListener("click", function () {
      document.documentElement.classList.toggle("dark");
      var dark = document.documentElement.classList.contains("dark");
      try { localStorage.setItem("lf-theme", dark ? "dark" : "light"); } catch (e) {}
    });
  }
  try {
    if (localStorage.getItem("lf-theme") === "dark") {
      document.documentElement.classList.add("dark");
    }
  } catch (e) {}

  // Sidebar collapse (desktop) persisted
  var sbBtn = document.getElementById("sidebar-toggle");
  if (sbBtn) {
    sbBtn.addEventListener("click", function () {
      document.body.classList.toggle("sb-collapsed");
      var c = document.body.classList.contains("sb-collapsed");
      try { localStorage.setItem("lf-sb", c ? "1" : "0"); } catch (e) {}
    });
  }
  try { if (localStorage.getItem("lf-sb") === "1") document.body.classList.add("sb-collapsed"); } catch (e) {}

  // Mobile sidebar drawer
  var mBtn = document.getElementById("sidebar-mobile-open");
  var overlay = document.getElementById("sidebar-mobile-overlay");
  if (mBtn && overlay) {
    mBtn.addEventListener("click", function () {
      document.body.classList.add("sb-open");
      overlay.classList.remove("hidden");
    });
    overlay.addEventListener("click", function () {
      document.body.classList.remove("sb-open");
      overlay.classList.add("hidden");
    });
  }
});

// Select-all checkbox for tables (bulk actions)
document.addEventListener("change", function (e) {
  if (e.target && e.target.id === "check-all") {
    var boxes = document.querySelectorAll("input[data-row-check]");
    boxes.forEach(function (b) { b.checked = e.target.checked; });
    updateBulkBar();
  }
  if (e.target && e.target.hasAttribute("data-row-check")) {
    updateBulkBar();
  }
});

function updateBulkBar() {
  var n = document.querySelectorAll("input[data-row-check]:checked").length;
  var bar = document.getElementById("bulk-bar");
  var count = document.getElementById("bulk-count");
  if (bar) {
    bar.classList.toggle("hidden", n === 0);
    if (count) count.textContent = n + " selected";
  }
}

// Kanban HTML5 drag & drop (progressive enhancement; menu fallback works)
document.addEventListener("DOMContentLoaded", function () {
  var cards = document.querySelectorAll("[data-draggable-card]");
  var cols = document.querySelectorAll("[data-stage-dropzone]");
  cards.forEach(function (c) {
    c.addEventListener("dragstart", function (e) {
      e.dataTransfer.setData("text/deal", c.getAttribute("data-deal-id"));
      if (e.dataTransfer.setData) e.dataTransfer.setData("text/plain", c.getAttribute("data-deal-id"));
    });
  });
  cols.forEach(function (col) {
    col.addEventListener("dragover", function (e) { e.preventDefault(); col.classList.add("kanban-placeholder"); });
    col.addEventListener("dragleave", function () { col.classList.remove("kanban-placeholder"); });
    col.addEventListener("drop", function (e) {
      e.preventDefault();
      col.classList.remove("kanban-placeholder");
      var dealId = e.dataTransfer.getData("text/deal") || e.dataTransfer.getData("text/plain");
      var stage = col.getAttribute("data-stage-id");
      if (dealId && stage) {
        var form = document.getElementById("kanban-move-form");
        if (form) {
          form.querySelector("[name=deal_id]").value = dealId;
          form.querySelector("[name=stage_id]").value = stage;
          htmx.trigger(form, "submit");
        }
      }
    });
  });
});

if (window.htmx) {
  document.body.addEventListener("kanbanMoved", function () { location.reload(); });
}

// Clickable rows/cards via data-href (ignores clicks on links/controls)
document.addEventListener("click", function (e) {
  if (!e.target || !e.target.closest) return;
  if (e.target.closest("a,button,input,select,textarea,summary")) return;
  var t = e.target.closest("[data-href]");
  if (t) location.href = t.getAttribute("data-href");
});

// Keyboard: Ctrl+K focus global search
document.addEventListener("keydown", function (e) {
  if ((e.ctrlKey || e.metaKey) && (e.key === "k" || e.key === "K")) {
    e.preventDefault();
    var input = document.getElementById("global-search-input");
    if (input) { input.focus(); input.select(); }
  }
});

// Confirm dialogs
document.addEventListener("submit", function (e) {
  var f = e.target;
  if (f && f.hasAttribute("data-confirm")) {
    if (!window.confirm(f.getAttribute("data-confirm"))) e.preventDefault();
  }
});

// Lead drawer: open when content swaps in, close on backdrop/button/Escape
function lfDrawer(open) {
  var d = document.querySelector(".drawer");
  var b = document.querySelector(".drawer-backdrop");
  if (!d) return;
  d.classList.toggle("translate-x-full", !open);
  if (b) b.classList.toggle("hidden", !open);
  document.body.style.overflow = open ? "hidden" : "";
}
document.addEventListener("click", function (e) {
  if (e.target && e.target.closest && e.target.closest("[data-drawer-close]")) lfDrawer(false);
});
document.addEventListener("keydown", function (e) {
  if (e.key === "Escape") lfDrawer(false);
});
if (window.htmx) {
  document.body.addEventListener("htmx:afterSwap", function (e) {
    if (e.detail && e.detail.target && e.detail.target.id === "drawer-host") {
      lfDrawer(!!e.detail.target.querySelector(".drawer"));
    }
  });
}
