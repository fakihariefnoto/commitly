// Theme toggle + filter bar, progressive enhancement only — the page is
// fully readable without this file.
(function () {
  "use strict";
  var root = document.documentElement;
  var btn = document.getElementById("theme-toggle");

  function apply(theme) {
    if (theme === "light" || theme === "dark") {
      root.setAttribute("data-theme", theme);
    } else {
      root.removeAttribute("data-theme");
    }
  }

  function current() {
    var stored = localStorage.getItem("commitly-theme");
    if (stored) return stored;
    return "auto";
  }

  apply(current());

  if (btn) {
    btn.addEventListener("click", function () {
      var c = current();
      var next = c === "dark" ? "light" : "dark";
      localStorage.setItem("commitly-theme", next);
      apply(next);
    });
  }
})();
