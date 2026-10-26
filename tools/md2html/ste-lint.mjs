#!/usr/bin/env node
// ste-lint: mechanical checks for the parts of ASD-STE100 and "no LLM tics"
// that a program can test. It does NOT check the approved-word dictionary or
// word meanings, so a clean run does not prove the text is STE-compliant.
//
// Errors fail the build. Warnings point at likely problems; fix them unless
// the flagged text is a technical name or a quotation.
//
// Scope: prose only. Code fences, inline code, and raw HTML blocks are skipped.

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import MarkdownIt from "markdown-it";

export const LIMITS = { descriptive: 25, procedural: 20, paragraph: 6 };

// [pattern, advice]. Applied to all prose: paragraphs, lists, tables, headings.
const BANNED = [
  [/\butili[sz](e|es|ed|ing|ation)\b/i, 'Use "use".'],
  [/\bleverag(e|es|ed|ing)\b/i, 'Use "use".'],
  [/\b(ensure|ensures|ensuring)\b/i, 'Use "make sure".'],
  [/\bfacilitat\w+/i, 'Use "help" or "allow".'],
  [/\bcommenc\w+/i, 'Use "start".'],
  [/\bsubsequent(ly)?\b/i, 'Use "next" or "then".'],
  [/\bprior to\b/i, 'Use "before".'],
  [/\bin order to\b/i, 'Use "to".'],
  [/\b(in the event that|due to the fact that|with regard to|in regards to)\b/i, "Use a shorter phrase."],
  [/\b(a number of|numerous)\b/i, "Give the number, or use \"many\"."],
  [/\b(delv(e|es|ed|ing)|tapestry|realm)\b/i, "LLM tic. Say the plain thing."],
  [/\b(crucial|pivotal|paramount)\b/i, "Empty emphasis. State the consequence instead."],
  [/\brobust(ness)?\b/i, "Empty praise. Say what it handles."],
  [/\bseamless(ly)?\b/i, "Empty praise. Say what happens."],
  [/\b(comprehensive(ly)?|holistic(ally)?)\b/i, "Empty praise. List what is covered."],
  [/\b(game-chang\w+|cutting-edge|state-of-the-art|best-in-class|world-class)\b/i, "Marketing language."],
  [/\b(streamlin\w+|unlock(s|ed|ing)?|empower\w*|supercharg\w+|revolutioni[sz]\w*)\b/i, "Marketing language."],
  [/\b(moreover|furthermore|additionally|nevertheless)\b/i, "Start a new sentence without the connector."],
  [/\b(worth (noting|mentioning)|important to (note|remember|understand)|it should be noted|note that|keep in mind|bear in mind)\b/i, "Filler. State the point directly."],
  [/\b(deep[- ]dive|dive (in|into|deeper))\b/i, "LLM tic."],
  [/\b(in conclusion|to summari[sz]e|in summary|at the end of the day|when it comes to|in today's|in the world of|a testament to)\b/i, "Filler."],
  [/\bplays? an? (crucial|key|vital|important) role\b/i, "Say what it does."],
  [/\b(very|really|quite|extremely|incredibly|basically|essentially|actually|simply|just|obviously|clearly|literally|of course)\b/i, "Filler word. Delete it."],
  [/\bnot (just|only|merely)\b[^.;:]{1,80}\bbut\b/i, 'Avoid the "not just X, but Y" pattern. Say Y.'],
  [/\b(should|might|could|would|shall)\b|\bmay\b(?! \d)/, 'Modal verb. Use "must" (required), "can" (possible), or "will".'],
  [/\b(e\.g\.|i\.e\.|etc\.?|and so on|and more|and\/or)/i, "Latin abbreviation or open list. Give the full list or say \"for example\"."],
  [/—|\s–\s|\s--\s/, "Dash. Split into two sentences, or use a colon."],
  [/!/, "No exclamation marks."],
];

const CONTRACTION =
  /\b\w+n't\b|\b(I|you|we|they)'(re|ve|ll|d|m)\b|\b(it|that|there|here|what|who|how|where|let|he|she)'s\b/i;

const SHORT_WORD_OK = /^(nothing|something|anything|everything|string|thing|ring|bring|spring|during|morning|evening|ceiling|king|wing|sing|sting|swing|ping|lightning)$/i;
const PROGRESSIVE = /\b(am|is|are|was|were|be|been|being)\s+([a-z]+ing)\b/i;
const PERFECT = /\b(has|have|had)\s+(?:been\s+)?([a-z]+ed|[a-z]+en|done|made|run|set|built|sent|found|kept|left|held|read|seen|shown|written|taken)\b/i;
const PASSIVE = /\b(is|are|was|were|be|been)\s+([a-z]+ed|written|given|shown|seen|done|made|taken|known|built|sent|found|kept|left|held)\b/i;

function plainText(children) {
  let s = "";
  for (const c of children ?? []) {
    if (c.type === "text") s += c.content;
    else if (c.type === "code_inline") s += "§";
    else if (c.type === "softbreak" || c.type === "hardbreak") s += " ";
  }
  return s.replace(/[\u2018\u2019]/g, "'").replace(/[\u201c\u201d]/g, '"').replace(/\s+/g, " ").trim();
}

const splitSentences = (text) =>
  text.split(/(?<=[.!?])\s+(?=[A-Z0-9"(\[§])/).filter((s) => /[\p{L}\p{N}]/u.test(s));

const wordCount = (s) => s.split(/\s+/).filter((w) => /[\p{L}\p{N}§]/u.test(w)).length;

const snippet = (text, m) => {
  const start = Math.max(0, m.index - 25);
  const end = Math.min(text.length, m.index + m[0].length + 25);
  return (start > 0 ? "…" : "") + text.slice(start, end) + (end < text.length ? "…" : "");
};

export function lintTokens(tokens) {
  const findings = [];
  const add = (severity, line, rule, message, text) =>
    findings.push({ severity, line, rule, message, text });

  const lists = [];
  let inCell = 0;

  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    switch (t.type) {
      case "ordered_list_open": lists.push("ol"); continue;
      case "bullet_list_open": lists.push("ul"); continue;
      case "ordered_list_close":
      case "bullet_list_close": lists.pop(); continue;
      case "th_open":
      case "td_open": inCell++; continue;
      case "th_close":
      case "td_close": inCell--; continue;
    }
    if (t.type !== "inline") continue;

    const kind =
      tokens[i - 1]?.type === "heading_open" ? "heading" : inCell > 0 ? "cell" : "prose";
    const line = (t.map?.[0] ?? tokens[i - 1]?.map?.[0] ?? 0) + 1;
    const text = plainText(t.children);
    if (!text) continue;

    // Checks that apply everywhere.
    for (const [re, advice] of BANNED) {
      const m = text.match(re);
      if (m) add("error", line, "banned", `"${m[0].trim()}": ${advice}`, snippet(text, m));
    }
    const c = text.match(CONTRACTION);
    if (c) add("error", line, "contraction", `"${c[0]}": write the full words.`, snippet(text, c));
    if (kind === "heading" && /\p{Extended_Pictographic}/u.test(text))
      add("warning", line, "emoji", "Emoji in heading.", text);

    if (kind !== "prose") continue;

    // Prose-only checks.
    const procedural = lists[lists.length - 1] === "ol";
    const limit = procedural ? LIMITS.procedural : LIMITS.descriptive;
    const sentences = splitSentences(text);

    if (sentences.length > LIMITS.paragraph)
      add("error", line, "paragraph", `${sentences.length} sentences. Maximum is ${LIMITS.paragraph}. Split it or use a list.`, text.slice(0, 80) + "…");

    for (const s of sentences) {
      const n = wordCount(s);
      if (n > limit)
        add("error", line, "sentence-length", `${n} words. Maximum is ${limit} (${procedural ? "procedure" : "description"}).`, s.length > 90 ? s.slice(0, 90) + "…" : s);
      if (s.includes(";"))
        add("warning", line, "semicolon", "Semicolon joins two ideas. Use two sentences.", s.slice(0, 90));
      const pr = s.match(PROGRESSIVE);
      if (pr && !SHORT_WORD_OK.test(pr[2]))
        add("warning", line, "tense", `"${pr[0]}": progressive tense. Use simple present, past, or future.`, snippet(s, pr));
      const pf = s.match(PERFECT);
      if (pf) add("warning", line, "tense", `"${pf[0]}": perfect tense. Use simple past or present.`, snippet(s, pf));
      const pv = s.match(PASSIVE);
      if (pv) add("warning", line, "passive", `"${pv[0]}": passive voice. Name the actor.`, snippet(s, pv));
    }
  }
  return findings;
}

export function formatFindings(findings, file = "") {
  return findings
    .map((f) => `${file}:${f.line}: ${f.severity} [${f.rule}] ${f.message}\n    ${f.text}`)
    .join("\n");
}

// Standalone: ste-lint <file.md>
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const file = process.argv[2];
  if (!file) {
    console.error("Usage: ste-lint <file.md>");
    process.exit(2);
  }
  const tokens = new MarkdownIt({ html: true }).parse(readFileSync(file, "utf8"), {});
  const findings = lintTokens(tokens);
  if (findings.length) console.error(formatFindings(findings, file));
  const errors = findings.filter((f) => f.severity === "error").length;
  console.error(`ste-lint: ${errors} error(s), ${findings.length - errors} warning(s)`);
  process.exit(errors ? 1 : 0);
}
