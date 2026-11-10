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

  // A form marked data-no-submit never navigates: its fields drive their
  // own htmx requests (the match dialog's search form).
  document.addEventListener("submit", function (event) {
    if (event.target.closest("[data-no-submit]")) {
      event.preventDefault();
    }
  });
})();
