/* ─────────────────────────────────────────────────────────────
 * ventd · schedule screen behaviour
 * Theme toggle (shared) + rule selection (visual only).
 * Strict-CSP friendly — no inline handlers.
 * ───────────────────────────────────────────────────────────── */

(function () {
  // theme toggle — same as every other screen
  var btn = document.getElementById("theme-toggle");
  if (btn) {
    btn.addEventListener("click", function () {
      var html = document.documentElement;
      var cur = html.getAttribute("data-theme") || "dark";
      html.setAttribute("data-theme", cur === "dark" ? "light" : "dark");
    });
  }

  // rule selection — clicking a .sched-rule moves .is-selected and updates the
  // detail panel header. The mock detail body stays as-is (we'd swap data in
  // a real impl).
  var list = document.querySelector(".sched-rule-list");
  var detailName = document.querySelector(".sched-detail-name");
  if (list) {
    list.addEventListener("click", function (e) {
      var row = e.target.closest(".sched-rule");
      if (!row) return;
      // toggles inside the row should not trigger selection
      if (e.target.closest(".sched-rule-toggle")) {
        e.target.closest(".sched-rule-toggle").classList.toggle("is-on");
        e.stopPropagation();
        return;
      }
      list.querySelectorAll(".sched-rule.is-selected").forEach(function (n) {
        n.classList.remove("is-selected");
      });
      row.classList.add("is-selected");
      var name = row.querySelector(".sched-rule-name");
      if (detailName && name) detailName.textContent = name.textContent;
    });
  }
})();
