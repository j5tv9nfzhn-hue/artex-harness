import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "@radix-ui/themes/styles.css";
import "./tokens.css";
import "./styles.css";
import App from "./App.jsx";

/* The design tokens in tokens.css are switched by `data-theme` on <html>:
 * `dark` selects the near-black ladder, anything else the light one. The
 * attribute is set here, before the first render, so the page never paints a
 * light frame and then flips — the reference keeps the same rule (dark tokens
 * are the base, light overrides live under a selector).
 *
 * `artex.appearance` is the reader's stored choice: "inherit" follows the OS. */
function applyTheme(choice) {
  const dark = choice === "dark"
    || (choice !== "light" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  document.documentElement.dataset.themeChoice = choice || "inherit";
  // React owns the Radix `Theme` appearance prop; it has to be told the answer
  // changed, or the wrapper keeps the class it rendered with and Radix's own
  // scale stays on the old scheme while the token ladder has already flipped.
  window.dispatchEvent(new CustomEvent("artex:theme", { detail: dark ? "dark" : "light" }));
}

let stored = "inherit";
try { stored = localStorage.getItem("artex.appearance") || "inherit"; } catch { /* private mode */ }
applyTheme(stored);

// Following the OS keeps working while the reader has not picked a scheme.
window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", () => {
  let choice = "inherit";
  try { choice = localStorage.getItem("artex.appearance") || "inherit"; } catch { /* ignore */ }
  if (choice === "inherit") applyTheme(choice);
});

// The settings page writes the same key; pick the change up without a reload.
window.addEventListener("artex:appearance", (e) => applyTheme(e.detail || "inherit"));

createRoot(document.getElementById("root")).render(
  <StrictMode><App /></StrictMode>,
);
