// Allister's page scripts. Every page loads this file (base.html, deferred,
// after htmx); the templates carry no inline handlers, so a strict
// Content-Security-Policy stays possible.
(function () {
  "use strict";

  // The dialog slot re-renders a dialog when its handler answers with one
  // of the error statuses listed in the slot's data-swap-statuses (htmx
  // swaps no 4xx or 5xx answer by itself). Only requests issued from
  // inside the slot are allowed, like the inline handler this replaces.
  document.addEventListener("htmx:beforeSwap", function (event) {
    var slot = document.getElementById("dialog-slot");
    if (!slot || !slot.contains(event.target)) {
      return;
    }
    var statuses = (slot.getAttribute("data-swap-statuses") || "").split(/\s+/);
    if (statuses.indexOf(String(event.detail.xhr.status)) !== -1) {
      event.detail.shouldSwap = true;
      event.detail.isError = false;
    }
  });

  // Closing a dialog removes it without following the cancel link (the
  // link stays a working GET for a page without JavaScript).
  document.addEventListener("click", function (event) {
    var close = event.target.closest("[data-dialog-close]");
    if (close) {
      event.preventDefault();
      var dialog = close.closest("[data-dialog]");
      if (dialog) {
        dialog.remove();
      }
    }
  });

  // The Series tab's expand control: htmx loads the parts into the series'
  // own tbody (a plain link to the series page without it); once they are
  // shown, a click empties the tbody again. Collapsed is the default.
  function seriesToggle(open, on) {
    open.setAttribute("aria-expanded", on ? "true" : "false");
    open.setAttribute("aria-label", open.getAttribute(on ? "data-label-open" : "data-label-closed"));
    var icon = open.querySelector(".icon");
    if (icon) {
      icon.textContent = on ? "expand_less" : "expand_more";
    }
  }
  document.addEventListener("click", function (event) {
    var toggle = event.target.closest("[data-series-toggle]");
    if (!toggle || toggle.getAttribute("aria-expanded") !== "true") {
      return;
    }
    event.preventDefault();
    event.stopPropagation(); // no second htmx request
    var row = toggle.closest("tbody");
    if (row && row.nextElementSibling) {
      row.nextElementSibling.replaceChildren();
    }
    seriesToggle(toggle, false);
  }, true);
  document.addEventListener("htmx:afterSwap", function (event) {
    var source = event.detail.requestConfig && event.detail.requestConfig.elt;
    if (!source || !source.matches("[data-series-toggle]")) {
      return; // another request that happened to swap into a tbody
    }
    var body = event.detail.target;
    var prev = body && body.previousElementSibling;
    var toggle = prev && prev.querySelector("[data-series-toggle]");
    if (toggle) {
      seriesToggle(toggle, true);
    }
  });

  // Appearance editor (progressive enhancement; the form works without
  // it): the colour picker and the #rrggbb field of a token follow each
  // other, and the page previews the colour at once through the token's
  // custom property (CSSOM, allowed under a strict style-src). Saving
  // applies it everywhere.
  document.addEventListener("input", function (event) {
    var el = event.target;
    var token = el.getAttribute && el.getAttribute("data-token");
    if (!token || !/^[a-z-]+$/.test(token)) {
      return;
    }
    var value = el.value.trim().toLowerCase();
    if (/^#[0-9a-f]{3}$/.test(value)) {
      value = "#" + value[1] + value[1] + value[2] + value[2] + value[3] + value[3];
    }
    if (!/^#[0-9a-f]{6}$/.test(value)) {
      return;
    }
    var other = document.getElementById((el.type === "color" ? "hex-" : "color-") + token);
    if (other && other !== el) {
      other.value = value;
    }
    var n = parseInt(value.slice(1), 16);
    document.documentElement.style.setProperty("--al-" + token, (n >> 16) + " " + ((n >> 8) & 255) + " " + (n & 255));
  });

  // "Use this browser's zone" (Settings > General) fills the time zone field
  // with the browser's IANA zone name.
  document.addEventListener("click", function (event) {
    var btn = event.target.closest("[data-use-browser-zone]");
    if (!btn) {
      return;
    }
    var field = document.getElementById("time_zone");
    if (field && window.Intl && Intl.DateTimeFormat) {
      field.value = Intl.DateTimeFormat().resolvedOptions().timeZone || "";
    }
  });

  // Enter in a search marked data-search-only (the pickers inside the series
  // dialog) runs its htmx search instead of submitting the enclosing form.
  document.addEventListener("keydown", function (event) {
    var el = event.target;
    if (event.key !== "Enter" || !el.matches || !el.matches("[data-search-only]")) {
      return;
    }
    event.preventDefault();
    el.dispatchEvent(new Event("search", { bubbles: true }));
  });

  // A form marked data-no-submit never navigates: its fields drive their
  // own htmx requests (the match dialog's search form).
  document.addEventListener("submit", function (event) {
    if (event.target.closest("[data-no-submit]")) {
      event.preventDefault();
    }
  });

  // Bulk edit (progressive enhancement; without it the bar is a plain form
  // for the ticked rows of the page). The bar floats (the classes in its
  // data-float-class) while something is ticked and hides otherwise; the
  // page select-all ticks every row; once every row of the page is ticked
  // and the filter matches more, "Select all N matching" switches the form
  // to scope=filter (any unticked row switches it back).
  function bulkRows() {
    return document.querySelectorAll('input[data-bulk-row][form="bulk-form"]');
  }
  function bulkUpdate() {
    var bar = document.getElementById("bulk-form");
    if (!bar) {
      return;
    }
    var rows = bulkRows();
    var ticked = 0;
    for (var i = 0; i < rows.length; i++) {
      if (rows[i].checked) {
        ticked++;
      }
    }
    var total = parseInt(bar.getAttribute("data-bulk-total"), 10) || 0;
    var all = rows.length > 0 && ticked === rows.length;
    var scope = bar.querySelector("[data-bulk-scope]");
    if (scope && !all) {
      scope.value = "ids";
    }
    var filter = scope && scope.value === "filter";
    var count = bar.querySelector("[data-bulk-count]");
    if (count) {
      count.textContent = filter ? "All " + total + " matching selected" : ticked + " selected";
    }
    var more = bar.querySelector("[data-bulk-all-matching]");
    if (more) {
      more.hidden = filter || !all || total <= rows.length;
    }
    var pageAll = document.querySelectorAll("[data-bulk-page-all]");
    for (var j = 0; j < pageAll.length; j++) {
      pageAll[j].checked = all;
      pageAll[j].indeterminate = ticked > 0 && !all;
    }
    var float = (bar.getAttribute("data-float-class") || "").split(/\s+/).filter(Boolean);
    for (var k = 0; k < float.length; k++) {
      bar.classList.toggle(float[k], ticked > 0);
    }
    bar.hidden = ticked === 0;
  }
  // only the value of the chosen action is shown (all of them without JS)
  function bulkShowValue(select) {
    var form = select.form;
    if (!form) {
      return;
    }
    var groups = form.querySelectorAll("[data-bulk-for]");
    for (var i = 0; i < groups.length; i++) {
      groups[i].hidden = groups[i].getAttribute("data-bulk-for") !== select.value;
    }
  }
  function bulkInit() {
    // a rows partial (filters, sort) brings the list's new parameters and
    // count; the bar outside the rows region takes them over
    var bar = document.getElementById("bulk-form");
    var state = document.querySelector("[data-bulk-state]");
    if (bar && state) {
      var total = state.getAttribute("data-bulk-total") || "0";
      bar.setAttribute("data-bulk-total", total);
      var list = bar.querySelector("[data-bulk-list]");
      if (list) {
        list.value = state.getAttribute("data-bulk-list") || "";
      }
      var more = bar.querySelector("[data-bulk-all-matching]");
      if (more) {
        more.textContent = "Select all " + total + " matching";
      }
    }
    var reveal = document.querySelectorAll("[data-bulk-reveal]");
    for (var i = 0; i < reveal.length; i++) {
      reveal[i].hidden = false;
    }
    var actions = document.querySelectorAll("[data-bulk-action]");
    for (var j = 0; j < actions.length; j++) {
      bulkShowValue(actions[j]);
    }
    bulkUpdate();
    // the list reloaded after a bulk change: its own URL in the address bar
    var notice = document.querySelector("[data-replace-url]");
    var url = notice && notice.getAttribute("data-replace-url");
    if (url && url.charAt(0) === "/" && url.charAt(1) !== "/" && window.history && history.replaceState) {
      history.replaceState(null, "", url);
    }
  }
  document.addEventListener("change", function (event) {
    var el = event.target;
    if (el.matches("[data-bulk-page-all]")) {
      var rows = bulkRows();
      for (var i = 0; i < rows.length; i++) {
        rows[i].checked = el.checked;
      }
      bulkUpdate();
    } else if (el.matches("[data-bulk-row]")) {
      bulkUpdate();
    } else if (el.matches("[data-bulk-action]")) {
      bulkShowValue(el);
    }
  });
  document.addEventListener("click", function (event) {
    var more = event.target.closest("[data-bulk-all-matching]");
    if (!more) {
      return;
    }
    var scope = document.querySelector("#bulk-form [data-bulk-scope]");
    if (scope) {
      scope.value = "filter";
    }
    bulkUpdate();
  });
  // The list's scroll sentinel (data-list-more) swaps itself for the next
  // page's rows. When the order moved while scrolling (a +1 brings an entry
  // to the top), an entry can come again: the later copy is dropped, so
  // every entry is loaded once. The bulk state below then counts the
  // appended rows (the select-all covers every loaded row).
  document.addEventListener("htmx:afterSwap", function (event) {
    var source = event.detail.requestConfig && event.detail.requestConfig.elt;
    if (!source || !source.closest || !source.closest("[data-list-more]")) {
      return;
    }
    var seen = {};
    var rows = bulkRows();
    for (var i = 0; i < rows.length; i++) {
      if (seen[rows[i].value]) {
        var el = rows[i].closest("[data-row], [data-card]");
        if (el) {
          el.remove();
        }
      } else {
        seen[rows[i].value] = true;
      }
    }
  });
  // a rows partial (filters, sort) brings a new bar and new rows; the
  // sentinel brings more rows
  document.addEventListener("htmx:afterSwap", bulkInit);
  bulkInit();

  // A spoiler (rendered notes and reviews) is hidden until clicked; Enter
  // or Space on the focused spoiler does the same. The click that reveals a
  // spoiler never follows a link inside it (the reader has not seen the
  // target yet); once revealed, its links work normally.
  document.addEventListener("click", function (event) {
    var spoiler = event.target.closest(".spoiler");
    if (!spoiler) {
      return;
    }
    if (!spoiler.classList.contains("revealed")) {
      event.preventDefault();
      spoiler.classList.add("revealed");
    } else if (!event.target.closest("a")) {
      spoiler.classList.remove("revealed");
    }
  });
  document.addEventListener("keydown", function (event) {
    if (event.key !== "Enter" && event.key !== " ") {
      return;
    }
    var spoiler = event.target.closest(".spoiler");
    if (spoiler && event.target === spoiler) {
      event.preventDefault();
      spoiler.classList.toggle("revealed");
    }
  });
})();
