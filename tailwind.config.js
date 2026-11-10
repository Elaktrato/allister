// Colours are theme tokens: every colour class the templates use is mapped
// (internal/appearance/tailwind-map.json) to a CSS custom property
// --al-<token> holding an "r g b" triplet, which /appearance.css sets from
// the chosen palette. Only mapped classes exist: theme.colors carries no
// palette, so a class outside the map generates nothing (and
// TestTemplatesUseOnlyThemedColours fails on it).
const map = require("./internal/appearance/tailwind-map.json");

const token = (name) => `rgb(var(--al-${name}) / <alpha-value>)`;
const keywords = { transparent: "transparent", current: "currentColor", inherit: "inherit" };
const themed = (classes) => {
  const out = { ...keywords };
  for (const [cls, name] of Object.entries(classes)) out[cls] = token(name);
  return out;
};

module.exports = {
  content: ["./internal/web/templates/**/*.html"],
  theme: {
    colors: keywords,
    backgroundColor: themed(map.backgroundColor),
    textColor: themed(map.textColor),
    borderColor: themed(map.borderColor),
    ringColor: themed(map.ringColor),
    accentColor: { auto: "auto", ...themed(map.accentColor) },
    extend: {
      fontFamily: {
        sans: [
          'var(--al-font, ui-sans-serif, system-ui, sans-serif, "Apple Color Emoji", "Segoe UI Emoji", "Segoe UI Symbol", "Noto Color Emoji")',
        ],
      },
    },
  },
  plugins: [],
};
