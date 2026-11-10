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

  // A form marked data-no-submit never navigates: its fields drive their
  // own htmx requests (the match dialog's search form).
  document.addEventListener("submit", function (event) {
    if (event.target.closest("[data-no-submit]")) {
      event.preventDefault();
    }
  });
})();
