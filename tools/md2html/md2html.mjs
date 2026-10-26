#!/usr/bin/env node
// md2html: Markdown -> ONE self-contained HTML file.
//   - GitHub styling (light/dark via prefers-color-scheme), CSS inlined
//   - Shiki syntax highlighting (github-light / github-dark), no JS at view time
//   - ```mermaid fences pre-rendered to inline SVG in headless Chromium
//   - local images inlined as data: URIs
// The output needs no network, no scripts, and no sibling files.

import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, resolve, basename, extname } from "node:path";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import MarkdownIt from "markdown-it";
import anchor from "markdown-it-anchor";
import footnote from "markdown-it-footnote";
import taskLists from "markdown-it-task-lists";
import alert from "markdown-it-github-alerts";
import { bundledLanguages, createHighlighter } from "shiki";
import { lintTokens, formatFindings } from "./ste-lint.mjs";

const require = createRequire(import.meta.url);
const HERE = dirname(fileURLToPath(import.meta.url));

const USAGE = `Usage: md2html <input.md> [-o out.html] [--title T] [--toc | --no-toc]

  -o, --output   Output path (default: <input> with .html). Use - for stdout.
  --title        <title> text (default: first # heading, else file name)
  --toc          Force a table of contents (default: auto, when 4+ h2/h3 headings)
  --no-toc       Never emit a table of contents
  --no-lint      Skip the ASD-STE100 prose lint (ste-lint). Reports must not use this.

Mermaid: use \`\`\`mermaid fences. A diagram that fails to parse fails the build.`;

const { values: opts, positionals } = parseArgs({
  allowPositionals: true,
  options: {
    output: { type: "string", short: "o" },
    title: { type: "string" },
    toc: { type: "boolean" },
    "no-toc": { type: "boolean" },
    "no-lint": { type: "boolean" },
    help: { type: "boolean", short: "h" },
  },
});

if (opts.help || positionals.length !== 1) {
  console.error(USAGE);
  process.exit(opts.help ? 0 : 2);
}

const inputPath = resolve(positionals[0]);
if (!existsSync(inputPath)) {
  console.error(`md2html: no such file: ${inputPath}`);
  process.exit(2);
}
const inputDir = dirname(inputPath);
const source = readFileSync(inputPath, "utf8");

const SHIKI_THEMES = { light: "github-light", dark: "github-dark" };
// Arial resolves to metric-compatible Liberation Sans in the container and to
// Arial/Helvetica on the viewer's machine, so label boxes sized at render time
// still fit when the SVG is displayed.
const MERMAID_FONT = "Arial, Helvetica, sans-serif";

const slugify = (s) =>
  s.trim().toLowerCase().replace(/[^\p{L}\p{N}\s-]/gu, "").replace(/\s+/g, "-");

const escapeHtml = (s) =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

const MIME = {
  ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
  ".webp": "image/webp", ".svg": "image/svg+xml", ".avif": "image/avif",
};

const md = new MarkdownIt({ html: true, linkify: true, typographer: false });
md.use(anchor, { slugify, permalink: anchor.permalink.headerLink({ safariReaderFix: true }) });
md.use(footnote);
md.use(taskLists, { enabled: false });
md.use(alert);

// ---- pass 1: parse, collect what needs async work ------------------------
const env = {};
const tokens = md.parse(source, env);

const fenceLang = (t) => t.info.trim().split(/\s+/)[0].toLowerCase();
const mermaidSources = [];
const langs = new Set();
for (const t of tokens) {
  if (t.type !== "fence") continue;
  const lang = fenceLang(t);
  if (lang === "mermaid") mermaidSources.push(t.content);
  else if (lang in bundledLanguages) langs.add(lang);
}

const warnings = [];

// ---- prose lint (fails fast, before any slow work) ------------------------
if (!opts["no-lint"]) {
  const findings = lintTokens(tokens);
  if (findings.length) console.error(formatFindings(findings, basename(inputPath)));
  const errors = findings.filter((f) => f.severity === "error").length;
  if (errors) {
    console.error(`md2html: ${errors} prose error(s). Fix them and run again. No file written.`);
    process.exit(1);
  }
}

// ---- Shiki ----------------------------------------------------------------
const highlighter = await createHighlighter({
  themes: Object.values(SHIKI_THEMES),
  langs: [...langs],
});

// ---- Mermaid --------------------------------------------------------------
async function renderMermaid(sources) {
  if (sources.length === 0) return [];
  const { chromium } = require("playwright-core");
  const executablePath =
    process.env.MD2HTML_BROWSER || process.env.IMPECCABLE_BROWSER || undefined;
  const browser = await chromium.launch({ executablePath, args: ["--no-sandbox"] });
  try {
    const page = await browser.newPage();
    await page.setContent('<!doctype html><html><body></body></html>');
    await page.addScriptTag({ path: require.resolve("mermaid/dist/mermaid.min.js") });
    return await page.evaluate(
      async ({ sources, font }) => {
        const out = [];
        let n = 0;
        for (const code of sources) {
          const pair = {};
          for (const [variant, theme] of [["light", "default"], ["dark", "dark"]]) {
            window.mermaid.initialize({
              startOnLoad: false, theme, fontFamily: font, securityLevel: "strict",
            });
            try {
              const { svg } = await window.mermaid.render(`mmd-${variant}-${n}`, code);
              pair[variant] = svg;
            } catch (e) {
              pair.error = String((e && e.message) || e);
              break;
            }
          }
          out.push(pair);
          n++;
        }
        return out;
      },
      { sources, font: MERMAID_FONT },
    );
  } finally {
    await browser.close();
  }
}

const diagrams = await renderMermaid(mermaidSources);
const failed = diagrams
  .map((d, i) =>
    d.error ? `diagram #${i + 1}:\n    ` + d.error.split("\n").slice(0, 4).join("\n    ") : null,
  )
  .filter(Boolean);
if (failed.length) {
  console.error("md2html: mermaid render failed:\n  " + failed.join("\n  "));
  process.exit(1);
}

// ---- render rules ---------------------------------------------------------
let diagramIndex = 0;
md.renderer.rules.fence = (toks, idx) => {
  const t = toks[idx];
  const lang = fenceLang(t);
  if (lang === "mermaid") {
    const d = diagrams[diagramIndex++];
    return (
      `<div class="mermaid-diagram">` +
      `<div class="mm-light">${d.light}</div><div class="mm-dark">${d.dark}</div></div>\n`
    );
  }
  if (lang in bundledLanguages) {
    return highlighter.codeToHtml(t.content, {
      lang, themes: SHIKI_THEMES, defaultColor: false,
    }) + "\n";
  }
  // Unknown or absent language: still themed, just uncolored.
  return highlighter.codeToHtml(t.content, {
    lang: "text", themes: SHIKI_THEMES, defaultColor: false,
  }) + "\n";
};

const defaultImage = md.renderer.rules.image;
md.renderer.rules.image = (toks, idx, options, e, self) => {
  const t = toks[idx];
  const src = t.attrGet("src") ?? "";
  if (/^(https?:)?\/\//i.test(src)) {
    warnings.push(`remote image not embedded (won't render offline): ${src}`);
  } else if (!src.startsWith("data:")) {
    const file = resolve(inputDir, decodeURIComponent(src.split(/[?#]/)[0]));
    const mime = MIME[extname(file).toLowerCase()];
    if (existsSync(file) && mime) {
      t.attrSet("src", `data:${mime};base64,${readFileSync(file).toString("base64")}`);
    } else {
      warnings.push(`image not found or unsupported type, left as-is: ${src}`);
    }
  }
  return defaultImage ? defaultImage(toks, idx, options, e, self) : self.renderToken(toks, idx, options);
};

// ---- title + TOC ----------------------------------------------------------
const headings = [];
for (let i = 0; i < tokens.length; i++) {
  const t = tokens[i];
  if (t.type === "heading_open") {
    const level = Number(t.tag.slice(1));
    const text = tokens[i + 1].children
      .filter((c) => c.type === "text" || c.type === "code_inline")
      .map((c) => c.content)
      .join("");
    headings.push({ level, text, slug: slugify(text) });
  }
}
const title =
  opts.title ?? headings.find((h) => h.level === 1)?.text ?? basename(inputPath, extname(inputPath));

const tocEntries = headings.filter((h) => h.level === 2 || h.level === 3);
const wantToc = opts["no-toc"] ? false : (opts.toc ?? tocEntries.length >= 4);

// markdown-it-anchor dedupes repeated slugs (-1, -2); mirror that in the TOC.
function tocHtml() {
  const seen = new Map();
  const items = tocEntries.map((h) => {
    const n = seen.get(h.slug) ?? 0;
    seen.set(h.slug, n + 1);
    const id = n === 0 ? h.slug : `${h.slug}-${n}`;
    return `<li class="toc-l${h.level}"><a href="#${escapeHtml(id)}">${escapeHtml(h.text)}</a></li>`;
  });
  return `<details class="toc" open><summary>Contents</summary><ul>${items.join("")}</ul></details>\n`;
}

const bodyHtml = md.renderer.render(tokens, md.options, env);

// ---- assemble -------------------------------------------------------------
const baseCss = readFileSync(require.resolve("github-markdown-css/github-markdown.css"), "utf8");
const extraCss = readFileSync(resolve(HERE, "extra.css"), "utf8");

const html = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>${escapeHtml(title)}</title>
<style>
${baseCss}
${extraCss}
</style>
</head>
<body>
<article class="markdown-body">
${wantToc ? tocHtml() : ""}${bodyHtml}</article>
</body>
</html>
`;

if (opts.output === "-") {
  process.stdout.write(html);
} else {
  const out = resolve(opts.output ?? inputPath.replace(/\.[^.\/]+$/, "") + ".html");
  writeFileSync(out, html);
  console.error(
    `md2html: wrote ${out} (${(html.length / 1024).toFixed(0)} KiB, ` +
      `${diagrams.length} diagram(s), ${langs.size} language(s))`,
  );
}
for (const w of warnings) console.error(`md2html: warning: ${w}`);
